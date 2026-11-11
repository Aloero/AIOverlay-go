package providers

import (
	"strings"

	"aioverlay/internal/domain"
	"encoding/json"
)

// Normalize OpenCode's structured error envelope; never show its raw JSON or
// responseBody (which can contain credentials and upstream payloads).
func openCodeError(raw json.RawMessage) *RequestError {
	var e struct {
		Name string `json:"name"`
		Data struct {
			StatusCode int    `json:"statusCode"`
			Code       string `json:"code"`
			Message    string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Malformed OpenCode error envelope"}
	}
	if e.Data.StatusCode != 0 {
		result := HTTPError(e.Data.StatusCode, "OpenCode provider request failed")
		result.Message = "OpenCode provider request failed"
		result.Detail = providerReason(e.Data.Message)
		if e.Data.Code == "region_restricted" || e.Data.Code == "unsupported_country_region_territory" {
			result.Outcome = domain.OutcomeRegionRestricted
		}
		return result
	}
	outcomes := map[string]domain.Outcome{
		"ProviderAuthError": domain.OutcomeAuthFailed, "ModelNotFoundError": domain.OutcomeModelUnavailable,
		"ProviderModelNotFoundError": domain.OutcomeModelUnavailable, "MessageAbortedError": domain.OutcomeCancelled,
		"ContextOverflowError": domain.OutcomeValidationFailed, "MessageOutputLengthError": domain.OutcomeInvalidResponse,
	}
	outcome, ok := outcomes[e.Name]
	if !ok {
		outcome = domain.OutcomeProviderUnavailable
	}
	return &RequestError{Outcome: outcome, Message: "OpenCode provider request failed", Detail: providerReason(e.Data.Message)}
}

// providerReason prepares one quoted reason for a trace. It is the only place a provider's own words
// survive into a report, so the bounds are the point: one line, no control characters, no long token
// that could be a key, and short enough to render in the overlay.
func providerReason(raw string) string {
	reason := strings.Join(strings.Fields(strings.ReplaceAll(raw, "\n", " ")), " ")
	if start := strings.IndexAny(reason, "{["); start >= 0 {
		reason = strings.TrimSpace(reason[:start]) // never a JSON fragment: it is where bodies hide
	}
	if index := strings.LastIndex(reason, "sk-"); index >= 0 {
		reason = strings.TrimSpace(reason[:index])
	}
	runes := []rune(reason)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return reason
}
