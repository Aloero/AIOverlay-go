package providers

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIPv4ProxyConnect(t *testing.T) {
	up, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		c, _ := up.Accept()
		if c != nil {
			defer c.Close()
			b := make([]byte, 4)
			io.ReadFull(c, b)
			c.Write([]byte("pong"))
		}
	}()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		c, _ := proxy.Accept()
		if c != nil {
			handleProxyConn(c)
		}
	}()
	c, err := net.DialTimeout("tcp4", proxy.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", up.Addr(), up.Addr())
	br := bufio.NewReader(c)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "200") {
		t.Fatalf("%q", line)
	}
	for {
		line, _ = br.ReadString('\n')
		if line == "\r\n" {
			break
		}
	}
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	io.ReadFull(br, b)
	if string(b) != "pong" {
		t.Fatalf("%q", b)
	}
}

// The proxy is the embedded runtime's only egress, so a refused CONNECT has to say why: an
// unanswered close arrives at the child as a bare connection reset and the provider can only
// report an unexplainable network error.
func TestIPv4ProxyRejectsExplicitly(t *testing.T) {
	for _, tc := range []struct{ name, target, want string }{
		{"no port", "aioverlay.invalid", "400"},
		{"unusable name", "aioverlay-nonexistent.invalid:443", "502"},
	} {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			handleProxyConn(c)
		}()
		c, err := net.DialTimeout("tcp4", ln.Addr().String(), time.Second)
		if err != nil {
			ln.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", tc.target, tc.target)
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		status, err := bufio.NewReader(c).ReadString('\n')
		c.Close()
		ln.Close()
		if err != nil {
			t.Fatalf("%s: the proxy closed without a status line: %v", tc.name, err)
		}
		if !strings.Contains(status, tc.want) {
			t.Fatalf("%s: wanted %s, got %q", tc.name, tc.want, status)
		}
	}
}
