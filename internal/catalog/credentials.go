package catalog

import "regexp"

var apiCredentialEnv = regexp.MustCompile(`(?i)(?:API_?KEY|AUTH_?TOKEN|ACCESS_?TOKEN|BEARER_?TOKEN|TOKEN|PASSWORD|SECRET|SECRET_?KEY)$`)

// Models.dev env lists may also contain regions, resource names, IDs or paths.
// Never populate those fields with a pasted API key. OpenCode additionally
// receives the key through provider.options.apiKey; special cloud identities
// are not converted to single-key authorization by this metadata helper.
func credentialEnvs(env []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range env {
		if apiCredentialEnv.MatchString(name) && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return out
}
