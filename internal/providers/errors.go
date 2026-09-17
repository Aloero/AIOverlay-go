package providers

import "strings"

func HTTPError(status int, message string) *RequestError {
	lower := strings.ToLower(message)
	e := &RequestError{StatusCode: status, Message: concise(message)}
	switch {
	case status == 401:
		e.Outcome = "auth_failed"
	case status == 402:
		e.Outcome = "quota_exceeded"
	case status == 403 && (strings.Contains(lower, "country") || strings.Contains(lower, "region")):
		e.Outcome = "region_restricted"
	case status == 403 || status == 404:
		e.Outcome = "model_unavailable"
	case status == 408:
		e.Outcome = "timeout"
		e.Retryable = true
	case status == 429:
		e.Outcome = "rate_limited"
		e.Retryable = true
	case status >= 500:
		e.Outcome = "provider_unavailable"
		e.Retryable = true
	default:
		e.Outcome = "invalid_response"
	}
	return e
}

func concise(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}
