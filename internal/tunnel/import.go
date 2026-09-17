package tunnel

// Share-link support is deliberately explicit. Never silently drop a transport
// or a security parameter that cannot be represented by the pinned sing-box.
import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type ImportedProfile struct {
	Name     string          `json:"name"`
	Config   json.RawMessage `json:"config"`
	Protocol string          `json:"protocol"`
	Server   string          `json:"server"`
	Warnings []string        `json:"warnings"`
}

func ImportProfile(raw string) (ImportedProfile, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 256<<10 {
		return ImportedProfile{}, fmt.Errorf("VPN profile is empty or exceeds 256 KiB")
	}
	result := ImportedProfile{Warnings: []string{}}
	var outbound map[string]any
	if strings.HasPrefix(raw, "{") {
		var err error
		outbound, err = extractOutbound(raw)
		if err != nil {
			return result, err
		}
		if _, err = BuildConfig(raw, 23456, "check", "check"); err != nil {
			return result, err
		}
		result.Config = json.RawMessage(raw)
		result.Name = "Основной"
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.Opaque != "" || strings.ContainsAny(u.Hostname(), "\r\n \t") {
			return result, fmt.Errorf("invalid VPN share link")
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return result, fmt.Errorf("invalid share-link parameters")
		}
		for _, values := range q {
			if len(values) != 1 {
				return result, fmt.Errorf("duplicate share-link parameters")
			}
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "vless" && scheme != "hysteria2" && scheme != "hy2" && scheme != "trojan" {
			return result, fmt.Errorf("supported links: vless://, hysteria2://, hy2://, trojan://; other profiles require sing-box JSON")
		}
		if u.Path != "" && u.Path != "/" {
			return result, fmt.Errorf("unexpected URI path; transport path belongs in the path parameter")
		}
		port := 443
		if u.Port() != "" {
			port, err = strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return result, fmt.Errorf("invalid server port; port hopping requires JSON")
			}
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.To4() == nil {
			return result, fmt.Errorf("VPN endpoint must support IPv4")
		}
		auth := ""
		if u.User != nil {
			auth = u.User.Username()
			if pass, ok := u.User.Password(); ok {
				auth += ":" + pass
			}
		}
		if auth == "" {
			return result, fmt.Errorf("share link has no authentication")
		}
		typ := scheme
		if scheme == "hy2" {
			typ = "hysteria2"
		}
		outbound = map[string]any{"type": typ, "server": u.Hostname(), "server_port": port}
		if strings.ContainsAny(auth, "\x00\r\n") {
			return result, fmt.Errorf("invalid authentication text")
		}
		if typ == "trojan" && (q.Get("encryption") != "" || q.Get("flow") != "") {
			return result, fmt.Errorf("VLESS-only parameters are not supported for Trojan")
		}
		if typ == "hysteria2" && q.Get("obfs-password") != "" && q.Get("obfs") == "" {
			return result, fmt.Errorf("obfuscation type is required")
		}
		if q.Get("allowInsecure") != "" && q.Get("insecure") != "" && q.Get("allowInsecure") != q.Get("insecure") {
			return result, fmt.Errorf("conflicting TLS verification flags")
		}
		allowed := map[string]bool{"sni": true, "alpn": true, "insecure": true}
		if typ == "hysteria2" {
			allowed["obfs"] = true
			allowed["obfs-password"] = true
			outbound["password"] = auth
			if obfs := q.Get("obfs"); obfs != "" {
				if obfs != "salamander" && obfs != "gecko" {
					return result, fmt.Errorf("unsupported Hysteria obfuscation")
				}
				if q.Get("obfs-password") == "" {
					return result, fmt.Errorf("obfuscation password is required")
				}
				outbound["obfs"] = map[string]any{"type": obfs, "password": q.Get("obfs-password")}
			}
			outbound["tls"] = map[string]any{"enabled": true, "server_name": u.Hostname()}
		} else {
			for _, name := range []string{"type", "security", "flow", "encryption", "fp", "pbk", "sid", "path", "host", "serviceName", "mode", "allowInsecure"} {
				allowed[name] = true
			}
			if typ == "vless" {
				if !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(auth) {
					return result, fmt.Errorf("VLESS link requires a UUID")
				}
				outbound["uuid"] = auth
				if enc := q.Get("encryption"); enc != "" && enc != "none" {
					return result, fmt.Errorf("this VLESS encryption is not supported by the bundled engine")
				}
				if flow := q.Get("flow"); flow != "" {
					if flow != "xtls-rprx-vision" {
						return result, fmt.Errorf("unsupported VLESS flow")
					}
					outbound["flow"] = flow
				}
			} else {
				outbound["password"] = auth
			}
			security := q.Get("security")
			if security == "" {
				if typ == "trojan" {
					security = "tls"
				} else {
					security = "none"
				}
			}
			if security != "reality" && (q.Get("pbk") != "" || q.Get("sid") != "") {
				return result, fmt.Errorf("REALITY parameters require security=reality")
			}
			if q.Get("flow") != "" && security == "none" {
				return result, fmt.Errorf("VLESS flow requires TLS/REALITY")
			}
			switch security {
			case "tls", "reality":
				tls := map[string]any{"enabled": true, "server_name": u.Hostname()}
				fp := q.Get("fp")
				if security == "reality" && fp == "" {
					fp = "chrome"
				}
				if fp != "" {
					tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
				}
				if security == "reality" {
					if q.Get("pbk") == "" {
						return result, fmt.Errorf("REALITY public key is required")
					}
					tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
				}
				outbound["tls"] = tls
			case "none":
				if typ == "trojan" {
					return result, fmt.Errorf("Trojan requires TLS")
				}
				result.Warnings = append(result.Warnings, "VLESS без TLS/REALITY: внешний туннель не шифруется этим протоколом.")
			default:
				return result, fmt.Errorf("unsupported transport security")
			}
			transport := q.Get("type")
			if transport == "" {
				transport = "tcp"
			}
			if (transport == "tcp" || transport == "raw") && (q.Get("path") != "" || q.Get("host") != "" || q.Get("serviceName") != "" || q.Get("mode") != "") {
				return result, fmt.Errorf("transport parameters supplied for a raw TCP link")
			}
			if transport != "grpc" && (q.Get("serviceName") != "" || q.Get("mode") != "") {
				return result, fmt.Errorf("gRPC parameters supplied for another transport")
			}
			if transport == "grpc" && (q.Get("path") != "" || q.Get("host") != "") {
				return result, fmt.Errorf("gRPC uses serviceName rather than path or host")
			}
			switch transport {
			case "tcp", "raw":
			case "ws":
				path := q.Get("path")
				if path == "" {
					path = "/"
				}
				tr := map[string]any{"type": "ws", "path": path}
				if host := q.Get("host"); host != "" {
					tr["headers"] = map[string]any{"Host": host}
				}
				outbound["transport"] = tr
			case "httpupgrade":
				path := q.Get("path")
				if path == "" {
					path = "/"
				}
				outbound["transport"] = map[string]any{"type": "httpupgrade", "host": q.Get("host"), "path": path}
			case "grpc":
				if mode := q.Get("mode"); mode != "" && mode != "gun" {
					return result, fmt.Errorf("only standard gRPC gun mode is supported")
				}
				outbound["transport"] = map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
			default:
				return result, fmt.Errorf("unsupported link transport; use a compatible sing-box outbound JSON")
			}
		}
		for key := range q {
			if !allowed[key] {
				return result, fmt.Errorf("share link has unsupported parameters; use a compatible sing-box JSON without silently dropping settings")
			}
		}
		if tls, ok := outbound["tls"].(map[string]any); ok {
			if sni := q.Get("sni"); sni != "" {
				tls["server_name"] = sni
			}
			if alpn := q.Get("alpn"); alpn != "" {
				tls["alpn"] = strings.Split(alpn, ",")
			}
			insecure := q.Get("insecure")
			if insecure == "" {
				insecure = q.Get("allowInsecure")
			}
			if insecure != "" && insecure != "0" && insecure != "1" && insecure != "true" && insecure != "false" {
				return result, fmt.Errorf("invalid TLS verification flag")
			}
			if insecure == "1" || insecure == "true" {
				tls["insecure"] = true
				result.Warnings = append(result.Warnings, "В профиле отключена проверка сертификата TLS.")
			}
		} else if q.Get("sni") != "" || q.Get("pbk") != "" || q.Get("fp") != "" || q.Get("sid") != "" || q.Get("alpn") != "" || q.Get("insecure") != "" || q.Get("allowInsecure") != "" {
			return result, fmt.Errorf("TLS parameters supplied without TLS/REALITY")
		}
		b, err := json.MarshalIndent(outbound, "", "  ")
		if err != nil {
			return result, fmt.Errorf("cannot encode VPN profile")
		}
		result.Config = b
		if _, err = BuildConfig(string(b), 23456, "check", "check"); err != nil {
			return result, err
		}
		result.Name = u.Fragment
		if result.Name == "" {
			result.Name = "Основной"
		}
		if len(result.Name) > 120 || strings.ContainsRune(result.Name, 0) {
			return result, fmt.Errorf("VPN profile name is too long or invalid")
		}
	}
	result.Protocol, _ = outbound["type"].(string)
	result.Server, _ = outbound["server"].(string)
	return result, nil
}
