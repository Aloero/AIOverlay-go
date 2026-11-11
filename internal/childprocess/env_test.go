package childprocess

import (
	"strings"
	"testing"
)

func TestSanitizedEnvDropsSecrets(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("OPENCODE_CONFIG", "/user/private/config.json")
	t.Setenv("NORMAL_SETTING", "not_inherited")
	env := SanitizedEnv()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "TEST_API_KEY=") {
		t.Fatal("secret leaked")
	}
	if !strings.Contains(joined, "LANG=C.UTF-8") {
		t.Fatal("normal env missing")
	}
	if strings.Contains(joined, "OPENCODE_CONFIG=") || strings.Contains(joined, "NORMAL_SETTING=") {
		t.Fatal("unexpected environment inherited")
	}
}
