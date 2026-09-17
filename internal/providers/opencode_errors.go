package providers

import (
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
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Malformed OpenCode error envelope"}
	}
	if e.Data.StatusCode != 0 {
		result := HTTPError(e.Data.StatusCode, "OpenCode provider request failed")
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
	return &RequestError{Outcome: outcome, Message: "OpenCode provider request failed"}
}
