package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strings"
)

func newDevelopmentToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("OS random source unavailable")
	}
	return hex.EncodeToString(b)
}
func validateListenAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("development API may only listen on a loopback IP")
	}
	return nil
}
func (s *Server) secureHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "Invalid local host", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "Cross-origin request denied", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
				http.Error(w, "Development API token required", 401)
				return
			}
			if r.Method == "POST" || r.Method == "PUT" {
				typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || typ != "application/json" {
					http.Error(w, "application/json required", 415)
					return
				}
			}
		} else if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) handleHotkeyEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := decodeJSON(r, &req, 1024); err != nil {
		http.Error(w, "invalid editor state", 400)
		return
	}
	if s.HotkeyEditor != nil {
		s.HotkeyEditor(req.Active)
	}
	writeJSON(w, map[string]bool{"ok": true})
}
