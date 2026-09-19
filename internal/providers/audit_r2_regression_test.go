package providers

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestR2AuditMalformedProxyURLDoesNotExposeSecret(t *testing.T) {
	_, err := requestTransport(context.Background(), testProxyResolver{url: "http://user:PRIVATE_PROXY_SECRET@127.0.0.1/%zz"}, time.Second)
	if err == nil {
		t.Fatal("malformed URL accepted")
	}
	if strings.Contains(err.Error(), "PRIVATE_PROXY_SECRET") {
		t.Fatalf("proxy parse leaked credential: %s", err)
	}
}
func TestR2AuditMalformedRealtimeURLDoesNotExposeToken(t *testing.T) {
	_, err := parseRealtimeEndpoint("wss://provider.example/%zz?token=PRIVATE_REALTIME_SECRET")
	if err == nil {
		t.Fatal("malformed URL accepted")
	}
	if strings.Contains(err.Error(), "PRIVATE_REALTIME_SECRET") {
		t.Fatalf("realtime URL parse leaked token: %s", err)
	}
}
func TestR2AuditConnectStatusDoesNotEchoProxyPassword(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		defer server.Close()
		_, err := http.ReadRequest(bufio.NewReader(server))
		if err == nil {
			fmt.Fprint(server, "HTTP/1.1 407 password=PRIVATE_PROXY_SECRET\r\nContent-Length: 0\r\n\r\n")
		}
	}()
	proxy, _ := url.Parse("http://user:PRIVATE_PROXY_SECRET@127.0.0.1:8888")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := dialHTTPConnect(ctx, func(context.Context, string, string) (net.Conn, error) { return client, nil }, proxy, "provider.example:443")
	if err == nil {
		t.Fatal("407 accepted")
	}
	if strings.Contains(err.Error(), "PRIVATE_PROXY_SECRET") {
		t.Fatalf("CONNECT error leaked password: %s", err)
	}
}
