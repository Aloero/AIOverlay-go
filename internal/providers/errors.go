package providers

import (
	"aioverlay/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"net"
)

// HTTPError never retains or displays a provider response body. Only documented
// structured codes are recognized; unknown codes use the HTTP status category.
func HTTPError(status int, body string) *RequestError {
	outcome := domain.OutcomeInvalidResponse
	switch {
	case status == 401:
		outcome = domain.OutcomeAuthFailed
	case status == 402:
		outcome = domain.OutcomeQuotaExceeded
	case status == 403 || status == 404:
		outcome = domain.OutcomeModelUnavailable
	case status == 408 || status == 504:
		outcome = domain.OutcomeTimeout
	case status == 429:
		outcome = domain.OutcomeRateLimited
	case status >= 500:
		outcome = domain.OutcomeProviderUnavailable
	}
	var envelope any
	if len(body) <= 65536 && json.Unmarshal([]byte(body), &envelope) == nil {
		codes := map[string]domain.Outcome{
			"API_KEY_INVALID": domain.OutcomeAuthFailed, "UNAUTHENTICATED": domain.OutcomeAuthFailed, "invalid_api_key": domain.OutcomeAuthFailed, "authentication_error": domain.OutcomeAuthFailed,
			"insufficient_quota": domain.OutcomeQuotaExceeded, "QUOTA_EXCEEDED": domain.OutcomeQuotaExceeded, "quota_exceeded": domain.OutcomeQuotaExceeded,
			"rate_limit_exceeded": domain.OutcomeRateLimited, "rate_limit_error": domain.OutcomeRateLimited,
			"unsupported_country_region_territory": domain.OutcomeRegionRestricted, "region_restricted": domain.OutcomeRegionRestricted, "LOCATION_POLICY_VIOLATED": domain.OutcomeRegionRestricted,
			"model_not_found": domain.OutcomeModelUnavailable, "UNAVAILABLE": domain.OutcomeProviderUnavailable,
		}
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for _, k := range []string{"error", "details", "errors"} {
					if child, ok := x[k]; ok {
						walk(child)
					}
				}
				for _, k := range []string{"reason", "code", "type", "status"} {
					if c, ok := x[k].(string); ok {
						if o, known := codes[c]; known {
							outcome = o
						}
					}
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		walk(envelope)
	}
	messages := map[domain.Outcome]string{domain.OutcomeAuthFailed: "Provider rejected the credential", domain.OutcomeQuotaExceeded: "Provider quota exhausted", domain.OutcomeRateLimited: "Provider rate limit reached", domain.OutcomeRegionRestricted: "Provider region policy rejected the request", domain.OutcomeModelUnavailable: "Model is unavailable or access was denied", domain.OutcomeTimeout: "Provider request timed out", domain.OutcomeProviderUnavailable: "Provider is temporarily unavailable", domain.OutcomeInvalidResponse: "Provider rejected the request"}
	return &RequestError{Outcome: outcome, StatusCode: status, Message: messages[outcome], Retryable: outcome == domain.OutcomeRateLimited || outcome == domain.OutcomeTimeout || outcome == domain.OutcomeProviderUnavailable}
}
func networkError(err error) *RequestError {
	outcome, message := domain.OutcomeNetworkError, "Provider network request failed"
	if errors.Is(err, context.Canceled) {
		outcome, message = domain.OutcomeCancelled, "Request cancelled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		outcome, message = domain.OutcomeTimeout, "Request timed out"
	} else {
		var n net.Error
		if errors.As(err, &n) && n.Timeout() {
			outcome, message = domain.OutcomeTimeout, "Network operation timed out"
		}
	}
	return &RequestError{Outcome: outcome, Message: message, Retryable: outcome != domain.OutcomeCancelled}
}
