package providers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealtimeDialFailsClosedBeforeNetworkWhenVPNResolverFails(t *testing.T) {
	var dials atomic.Int32
	connector := newRealtimeConnector(testProxyResolver{err: errors.New("tunnel down")}, time.Second)
	connector.dialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("unexpected dial")
	}

	if _, err := connector.DialTLS(context.Background(), "wss://provider.example/realtime"); err == nil {
		t.Fatal("broken AI VPN was ignored")
	}
	if dials.Load() != 0 {
		t.Fatalf("direct network dial occurred %d times", dials.Load())
	}
}

func TestRealtimeProxyTunnelUsesAuthenticatedConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	requestSeen := make(chan *http.Request, 1)
	serverDone := make(chan error, 1)
	go acceptConnectRequest(listener, requestSeen, serverDone)

	proxy, err := url.Parse("http://user:pass@" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dialer := (&net.Dialer{Timeout: time.Second}).DialContext
	connection, err := dialHTTPConnect(context.Background(), dialer, proxy, "provider.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()

	request := <-requestSeen
	if request.Method != http.MethodConnect || request.Host != "provider.example:443" {
		t.Fatalf("CONNECT request=%s %s", request.Method, request.Host)
	}
	if got := request.Header.Get("Proxy-Authorization"); got == "" {
		t.Fatal("authenticated proxy CONNECT omitted credentials")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func acceptConnectRequest(listener net.Listener, requestSeen chan<- *http.Request, done chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		done <- err
		return
	}
	defer connection.Close()
	request, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		done <- err
		return
	}
	requestSeen <- request
	_, err = connection.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	done <- err
}

func TestRealtimeEndpointRequiresWSS(t *testing.T) {
	unsafeEndpoints := []string{
		"ws://provider.example/realtime",
		"https://provider.example/realtime",
		"wss://user:secret@provider.example/realtime",
	}
	for _, endpoint := range unsafeEndpoints {
		if _, err := parseRealtimeEndpoint(endpoint); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
	if _, err := parseRealtimeEndpoint("wss://provider.example/realtime"); err != nil {
		t.Fatal(err)
	}
}

func TestRealtimeProxyResolverErrorDoesNotExposeCredentials(t *testing.T) {
	const secret = "proxy-password-do-not-leak"
	resolverErr := errors.New("proxy http://alice:" + secret + "@127.0.0.1:1080 unavailable")
	connector := newRealtimeConnector(testProxyResolver{err: resolverErr}, time.Second)
	var dials atomic.Int32
	connector.dialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	}

	_, err := connector.DialTLS(context.Background(), "wss://provider.example/realtime")
	if err == nil {
		t.Fatal("resolver failure was ignored")
	}
	public := err.Error()
	for _, forbidden := range []string{secret, "alice", "http://", "127.0.0.1:1080"} {
		if strings.Contains(public, forbidden) {
			t.Fatalf("proxy resolver detail leaked %q in %q", forbidden, public)
		}
	}
	if dials.Load() != 0 {
		t.Fatalf("resolver failure allowed %d network dials", dials.Load())
	}
}
