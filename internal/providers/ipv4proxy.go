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
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return
	}
	up, err := (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].String(), port))
	if err != nil {
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
func shutdownIPv4Proxy() {
	if sharedIPv4Proxy.ln != nil {
		_ = sharedIPv4Proxy.ln.Close()
	}
}

var _ = fmt.Sprintf
