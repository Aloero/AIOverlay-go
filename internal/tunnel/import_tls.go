package tunnel

import (
	"fmt"
	"strings"
)

func (link *shareProfile) configureTLS(result *ImportedProfile) error {
	q, outbound, u, typ := link.query, link.outbound, link.url, link.kind
	security := q.Get("security")
	if security == "" {
		if typ == "trojan" {
			security = "tls"
		} else {
			security = "none"
		}
	}
	if security != "reality" && (q.Get("pbk") != "" || q.Get("sid") != "") {
		return fmt.Errorf("REALITY parameters require security=reality")
	}
	if q.Get("flow") != "" && security == "none" {
		return fmt.Errorf("VLESS flow requires TLS/REALITY")
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
				return fmt.Errorf("REALITY public key is required")
			}
			tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
		}
		outbound["tls"] = tls
	case "none":
		if typ == "trojan" {
			return fmt.Errorf("Trojan requires TLS")
		}
		result.Warnings = append(result.Warnings, "VLESS без TLS/REALITY: внешний туннель не шифруется этим протоколом.")
	default:
		return fmt.Errorf("unsupported transport security")
	}
	return nil
}

func (link *shareProfile) applyTLSParameters(result *ImportedProfile) error {
	q, outbound := link.query, link.outbound
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
			return fmt.Errorf("invalid TLS verification flag")
		}
		if insecure == "1" || insecure == "true" {
			tls["insecure"] = true
			result.Warnings = append(result.Warnings, "В профиле отключена проверка сертификата TLS.")
		}
	} else if q.Get("sni") != "" || q.Get("pbk") != "" || q.Get("fp") != "" || q.Get("sid") != "" || q.Get("alpn") != "" || q.Get("insecure") != "" || q.Get("allowInsecure") != "" {
		return fmt.Errorf("TLS parameters supplied without TLS/REALITY")
	}
	return nil
}
