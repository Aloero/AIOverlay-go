package tunnel

import (
	"fmt"
	"regexp"
)

func (link *shareProfile) configureProtocol(result *ImportedProfile) error {
	typ, q := link.kind, link.query
	allowed := map[string]bool{"sni": true, "alpn": true, "insecure": true}
	if typ == "hysteria2" {
		allowed["obfs"] = true
		allowed["obfs-password"] = true
		if err := link.configureHysteria(); err != nil {
			return err
		}
	} else {
		for _, name := range []string{"type", "security", "flow", "encryption", "fp", "pbk", "sid", "path", "host", "serviceName", "mode", "allowInsecure"} {
			allowed[name] = true
		}
		if err := link.configureStreamAuth(); err != nil {
			return err
		}
		if err := link.configureTLS(result); err != nil {
			return err
		}
		if err := link.configureTransport(); err != nil {
			return err
		}
	}
	for key := range q {
		if !allowed[key] {
			return fmt.Errorf("share link has unsupported parameters; use a compatible sing-box JSON without silently dropping settings")
		}
	}
	return link.applyTLSParameters(result)
}

func (link *shareProfile) configureHysteria() error {
	q, outbound, auth, u := link.query, link.outbound, link.auth, link.url
	outbound["password"] = auth
	if obfs := q.Get("obfs"); obfs != "" {
		if obfs != "salamander" && obfs != "gecko" {
			return fmt.Errorf("unsupported Hysteria obfuscation")
		}
		if q.Get("obfs-password") == "" {
			return fmt.Errorf("obfuscation password is required")
		}
		outbound["obfs"] = map[string]any{"type": obfs, "password": q.Get("obfs-password")}
	}
	outbound["tls"] = map[string]any{"enabled": true, "server_name": u.Hostname()}
	return nil
}

func (link *shareProfile) configureStreamAuth() error {
	q, outbound, auth, typ := link.query, link.outbound, link.auth, link.kind
	if typ == "vless" {
		if !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(auth) {
			return fmt.Errorf("VLESS link requires a UUID")
		}
		outbound["uuid"] = auth
		if enc := q.Get("encryption"); enc != "" && enc != "none" {
			return fmt.Errorf("this VLESS encryption is not supported by the bundled engine")
		}
		if flow := q.Get("flow"); flow != "" {
			if flow != "xtls-rprx-vision" {
				return fmt.Errorf("unsupported VLESS flow")
			}
			outbound["flow"] = flow
		}
	} else {
		outbound["password"] = auth
	}
	return nil
}
