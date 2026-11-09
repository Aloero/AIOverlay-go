package tunnel

import "fmt"

func (link *shareProfile) configureTransport() error {
	q, outbound := link.query, link.outbound
	transport := q.Get("type")
	if transport == "" {
		transport = "tcp"
	}
	if (transport == "tcp" || transport == "raw") && (q.Get("path") != "" || q.Get("host") != "" || q.Get("serviceName") != "" || q.Get("mode") != "") {
		return fmt.Errorf("transport parameters supplied for a raw TCP link")
	}
	if transport != "grpc" && (q.Get("serviceName") != "" || q.Get("mode") != "") {
		return fmt.Errorf("gRPC parameters supplied for another transport")
	}
	if transport == "grpc" && (q.Get("path") != "" || q.Get("host") != "") {
		return fmt.Errorf("gRPC uses serviceName rather than path or host")
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
			return fmt.Errorf("only standard gRPC gun mode is supported")
		}
		outbound["transport"] = map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
	default:
		return fmt.Errorf("unsupported link transport; use a compatible sing-box outbound JSON")
	}
	return nil
}
