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

func TestSanitizedEnvDropsSecrets(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("OPENCODE_CONFIG", "/user/private/config.json")
	t.Setenv("NORMAL_SETTING", "not_inherited")
	env := sanitizedEnv()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "TEST_API_KEY=") {
		t.Fatal("secret leaked")
	}
	if !strings.Contains(joined, "LANG=C.UTF-8") {
		t.Fatal("normal env missing")
	}
	if strings.Contains(joined, "OPENCODE_CONFIG=") || strings.Contains(joined, "NORMAL_SETTING=") {
		t.Fatal("unexpected environment inherited")
	}
}
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
