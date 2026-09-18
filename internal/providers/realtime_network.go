package providers

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type realtimeConnector struct {
	resolver    ProxyResolver
	dialContext dialContextFunc
	tlsConfig   *tls.Config
}

func newRealtimeConnector(resolver ProxyResolver, connectTimeout time.Duration) realtimeConnector {
	dialer := &net.Dialer{Timeout: connectTimeout}
	return realtimeConnector{resolver: resolver, dialContext: dialer.DialContext}
}

func (c realtimeConnector) DialTLS(ctx context.Context, endpoint string) (net.Conn, error) {
	target, err := parseRealtimeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	proxy, err := resolveRealtimeProxy(ctx, c.resolver)
	if err != nil {
		return nil, err
	}

	authority := target.Host
	if target.Port() == "" {
		authority = net.JoinHostPort(target.Hostname(), "443")
	}
	connection, err := c.dialTarget(ctx, proxy, authority)
	if err != nil {
		return nil, err
	}

	tlsConfig := c.tlsConfiguration(target.Hostname())
	tlsConnection := tls.Client(connection, tlsConfig)
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		connection.Close()
		return nil, fmt.Errorf("realtime TLS handshake: %w", err)
	}
	return tlsConnection, nil
}

func (c realtimeConnector) dialTarget(ctx context.Context, proxy *url.URL, authority string) (net.Conn, error) {
	if proxy == nil {
		return c.dialContext(ctx, "tcp", authority)
	}
	return dialHTTPConnect(ctx, c.dialContext, proxy, authority)
}

func (c realtimeConnector) tlsConfiguration(serverName string) *tls.Config {
	if c.tlsConfig == nil {
		return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	}
	config := c.tlsConfig.Clone()
	if config.ServerName == "" {
		config.ServerName = serverName
	}
	return config
}

func parseRealtimeEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("realtime endpoint: %w", err)
	}
	if endpoint.Scheme != "wss" || endpoint.Hostname() == "" || endpoint.User != nil {
		return nil, fmt.Errorf("realtime endpoint must be a credential-free wss URL")
	}
	return endpoint, nil
}

func resolveRealtimeProxy(ctx context.Context, resolver ProxyResolver) (*url.URL, error) {
	if resolver == nil {
		return nil, nil
	}
	raw, err := resolver.ProxyURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("AI VPN unavailable")
	}
	if raw == "" {
		return nil, nil
	}
	proxy, err := url.Parse(raw)
	if err != nil || proxy.Scheme != "http" || proxy.Hostname() == "" {
		return nil, fmt.Errorf("AI VPN proxy URL is not a supported HTTP CONNECT proxy")
	}
	return proxy, nil
}

func dialHTTPConnect(ctx context.Context, dial dialContextFunc, proxy *url.URL, authority string) (net.Conn, error) {
	proxyAuthority := proxy.Host
	if proxy.Port() == "" {
		proxyAuthority = net.JoinHostPort(proxy.Hostname(), "80")
	}
	connection, err := dial(ctx, "tcp", proxyAuthority)
	if err != nil {
		return nil, fmt.Errorf("AI VPN proxy connect: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
		defer connection.SetDeadline(time.Time{})
	}

	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: authority}, Host: authority, Header: make(http.Header)}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		credentials := proxy.User.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := request.WriteProxy(connection); err != nil {
		connection.Close()
		return nil, fmt.Errorf("AI VPN CONNECT request: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("AI VPN CONNECT response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		connection.Close()
		return nil, fmt.Errorf("AI VPN CONNECT rejected: %s", strings.TrimSpace(response.Status))
	}
	return connection, nil
}
