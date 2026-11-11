package providers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type ipv4Proxy struct {
	once sync.Once
	ln   net.Listener
	url  string
	err  error
}

var sharedIPv4Proxy ipv4Proxy

func ipv4ProxyURL() (string, error) {
	sharedIPv4Proxy.once.Do(func() { sharedIPv4Proxy.start() })
	return sharedIPv4Proxy.url, sharedIPv4Proxy.err
}
func (p *ipv4Proxy) start() {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		p.err = err
		return
	}
	p.ln = ln
	p.url = "http://" + ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleProxyConn(c)
		}
	}()
}
func handleProxyConn(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(45 * time.Second))
	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 3 || strings.ToUpper(parts[0]) != "CONNECT" {
		io.WriteString(client, "HTTP/1.1 405 Method Not Allowed\r\nConnection: close\r\n\r\n")
		return
	}
	target := parts[1]
	for {
		h, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if h == "\r\n" || h == "\n" {
			break
		}
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		rejectProxy(client, "400 Bad Request", "malformed CONNECT target")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		rejectProxy(client, "502 Bad Gateway", "IPv4 lookup failed: "+err.Error())
		return
	}
	if len(ips) == 0 {
		// The app forces every child request through here, so a name with only AAAA records
		// would otherwise look like a provider outage.
		rejectProxy(client, "502 Bad Gateway", "no IPv4 address for "+host)
		return
	}
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	var up net.Conn
	for _, ip := range ips {
		up, err = dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
		if err == nil {
			break
		}
	}
	if up == nil {
		rejectProxy(client, "502 Bad Gateway", "connect failed: "+err.Error())
		return
	}
	defer up.Close()
	_ = client.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); done <- struct{}{} }()
	<-done
}

// rejectProxy answers a CONNECT this proxy cannot fulfil. Closing the socket without a status
// line reaches the child as a bare connection reset, which the provider can only report as an
// unexplainable network error - and this proxy is the child's only egress. A 502 with a reason
// makes the cause visible where the request fails.
func rejectProxy(client io.Writer, status, reason string) {
	body := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s", status, len(reason), reason)
	_, _ = io.WriteString(client, body)
}
