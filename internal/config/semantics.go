package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"
)

func enum[T ~string](v T, values ...T) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func within(v, min, max int) bool { return v >= min && v <= max }
func score(v float64) bool        { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100 }
func validateProvider(p domain.ProviderConfig) error {
	if !enum(p.Kind, domain.ProviderOpenAICompatible, domain.ProviderGemini, domain.ProviderEmbeddedOpenCode) {
		return fmt.Errorf("provider %s has unsupported protocol", p.ID)
	}
	if !enum(p.CredentialPool.Policy, domain.CredentialRoundRobin, domain.CredentialSequential, domain.CredentialSticky, domain.CredentialRandom) {
		return fmt.Errorf("provider %s has invalid key policy", p.ID)
	}
	if !within(p.CredentialPool.MaxKeysPerRequest, 0, 1000) || !within(p.CredentialPool.RateLimitCooldownS, 0, 86400) {
		return fmt.Errorf("provider key limits invalid")
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Hostname() == "" || !enum(u.Scheme, "https", "http") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("provider %s has invalid base URL", p.ID)
		}
	} else if p.Kind != domain.ProviderEmbeddedOpenCode && p.Enabled {
		return fmt.Errorf("provider %s requires base URL", p.ID)
	}
	for _, name := range p.CredentialEnv {
		upper := strings.ToUpper(name)
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
			return fmt.Errorf("invalid credential environment name")
		}
		if strings.HasPrefix(upper, "OPENCODE_") || strings.HasPrefix(upper, "XDG_") || enum(upper, "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR", "NODE_OPTIONS", "BUN_OPTIONS", "SSL_CERT_FILE", "SSL_CERT_DIR", "LD_PRELOAD", "LD_LIBRARY_PATH") {
			return fmt.Errorf("credential environment name conflicts with runtime isolation")
		}
	}

	d := p.RequestDefaults
	for _, v := range []int{d.ConnectTimeoutMS, d.FirstTokenTimeoutMS, d.ResponseTimeoutMS, d.StreamIdleTimeoutMS} {
		if !within(v, 0, 600000) {
			return fmt.Errorf("provider timeout out of range")
		}
	}
	if !within(d.MaxOutputTokens, 0, 2000000) {
		return fmt.Errorf("provider output token limit invalid")
	}
	return nil
}
func validateApplication(c domain.Config) error {
	for _, name := range []string{c.App.Name, c.Build.ProductName, c.Build.Company, c.Build.FileDescription} {
		if len(name) > 1024 || strings.ContainsRune(name, 0) {
			return fmt.Errorf("invalid application identity text")
		}
	}
	if strings.ContainsAny(c.Build.ExecutableName, "<>:\"/\\|?*\x00") || strings.TrimSpace(c.Build.ExecutableName) == "" {
		return fmt.Errorf("build executable must be a plain Windows filename")
	}

	if !within(c.App.CatalogRefreshHours, 0, 24*365) {
		return fmt.Errorf("catalog refresh interval out of range")
	}
	if !within(c.Overlay.Width, 240, 16000) || !within(c.Overlay.Height, 120, 16000) || !within(c.Overlay.FontSize, 6, 96) || !within(c.Overlay.StationaryMS, 0, 86400000) {
		return fmt.Errorf("overlay dimensions, font or stationary delay out of range")
	}
	host, _, err := net.SplitHostPort(c.ControlCenter.ListenAddress)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("development listener must use a loopback IP address")
	}
	for _, l := range c.ControlCenter.Layout {
		if math.IsNaN(l.Zoom) || math.IsInf(l.Zoom, 0) || l.Zoom < .25 || l.Zoom > 1.6 || math.Abs(l.PanX) > 1e6 || math.Abs(l.PanY) > 1e6 {
			return fmt.Errorf("invalid canvas viewport")
		}
	}
	return nil
}
func validateBudget(b domain.ExecutionBudget) error {
	if !within(b.MaxWallTimeMS, 1, 600000) || !within(b.MaxModelCalls, 1, 10000) || !within(b.MaxParallelCalls, 1, 64) {
		return fmt.Errorf("execution budget out of range")
	}
	if !within(b.MaxProviders, 0, 1000) || !within(b.MaxKeysPerProvider, 0, 1000) {
		return fmt.Errorf("negative or excessive provider/key budget")
	}
	for _, v := range []int{b.ConnectTimeoutMS, b.FirstTokenTimeoutMS, b.ResponseTimeoutMS, b.StreamIdleTimeoutMS} {
		if !within(v, 0, 600000) {
			return fmt.Errorf("timeout out of range")
		}
	}
	return nil
}
func validateNode(n domain.Node) error {
	if !enum(n.Type, domain.NodeScreen, domain.NodeModelPool, domain.NodeValidator, domain.NodeOutput) {
		return fmt.Errorf("unknown node type")
	}
	if n.Validator != nil {
		v := n.Validator
		if !enum(v.Kind, "", "nonempty", "regex", "length") {
			return fmt.Errorf("invalid validator kind")
		}
		if v.MinLength < 0 || v.MaxLength < 0 || (v.MaxLength > 0 && v.MaxLength < v.MinLength) {
			return fmt.Errorf("invalid validator length")
		}
		if len(v.Pattern) > 4096 {
			return fmt.Errorf("validator pattern too long")
		}
		if _, err := regexp.Compile(v.Pattern); err != nil {
			return fmt.Errorf("invalid validator regular expression")
		}
	}
	return nil
}
func validateEdge(e domain.Edge) error {
	if !within(e.MaxVisits, 1, 10000) || !within(e.DelayMS, 0, 600000) || len(e.On) == 0 {
		return fmt.Errorf("invalid transition limits or empty outcomes")
	}
	for _, o := range e.On {
		if !enum(o, domain.OutcomeSuccess, domain.OutcomeAnyError, domain.OutcomeAuthFailed, domain.OutcomeRateLimited, domain.OutcomeQuotaExceeded, domain.OutcomeTimeout, domain.OutcomeFirstTokenTimeout, domain.OutcomeStreamIdleTimeout, domain.OutcomeNetworkError, domain.OutcomeModelUnavailable, domain.OutcomeRegionRestricted, domain.OutcomeUnsupportedModality, domain.OutcomeProviderUnavailable, domain.OutcomeInvalidResponse, domain.OutcomeValidationFailed, domain.OutcomeCancelled) {
			return fmt.Errorf("unknown transition outcome")
		}
	}
	return nil
}
func validatePoolLimits(p *domain.ModelPoolConfig) error {
	if !enum(p.Policy, domain.PoolRoundRobin, domain.PoolPriority, domain.PoolParallel, domain.PoolStaggered, domain.PoolFastest, domain.PoolWeighted) {
		return fmt.Errorf("unknown dispatch policy")
	}
	if !within(p.MaxCalls, 1, 10000) || !within(p.MaxParallel, 1, 64) || !within(p.HedgeDelayMS, 0, 600000) || !score(p.QualityFloor) {
		return fmt.Errorf("pool limits out of range")
	}
	return nil
}
func validateSlot(m domain.ModelSlot) error {
	if !within(m.Priority, -1000000, 1000000) || len(m.ModelID) > 2048 || !score(m.QualityScore) || math.IsNaN(m.Weight) || math.IsInf(m.Weight, 0) || m.Weight < 0 || m.Weight > 1e6 || !within(m.MaxAttempts, 0, 100) || !within(m.TimeoutMS, 0, 600000) || !within(m.FirstTokenTimeoutMS, 0, 600000) {
		return fmt.Errorf("model slot limits or quality invalid")
	}
	if strings.ContainsRune(m.ModelID, 0) {
		return fmt.Errorf("model id contains NUL")
	}
	return nil
}
func reachable(p domain.Pipeline) bool {
	seen := map[string]bool{}
	queue := []string{p.EntryNodeID}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == p.OutputNodeID {
			return true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		for _, e := range p.Edges {
			if e.From == n {
				queue = append(queue, e.To)
			}
		}
	}
	return false
}
