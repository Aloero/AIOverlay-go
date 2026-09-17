package engine

import (
	"testing"
	"time"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func newPolicyEngine() *Engine {
	cfg := config.Default()
	reg, _ := providers.NewRegistry(cfg)
	return New(cfg, reg, nil, nil)
}
func slots() []domain.ModelSlot {
	return []domain.ModelSlot{{ID: "a", ProviderID: "p", ModelID: "a", Priority: 100, Weight: 1}, {ID: "b", ProviderID: "p", ModelID: "b", Priority: 90, Weight: 1}, {ID: "c", ProviderID: "p", ModelID: "c", Priority: 80, Weight: 1}}
}
func TestModelRoundRobinChangesFirst(t *testing.T) {
	e := newPolicyEngine()
	a := e.orderModelsForPolicy("n", domain.PoolRoundRobin, slots())
	b := e.orderModelsForPolicy("n", domain.PoolRoundRobin, slots())
	if a[0].ID == b[0].ID {
		t.Fatalf("expected rotation: %s %s", a[0].ID, b[0].ID)
	}
}
func TestCredentialRoundRobinAndSticky(t *testing.T) {
	e := newPolicyEngine()
	pool := domain.CredentialPool{Policy: domain.CredentialRoundRobin, Keys: []domain.Credential{{ID: "a", Secret: "1", Enabled: true, Priority: 100}, {ID: "b", Secret: "2", Enabled: true, Priority: 90}}}
	a := e.orderCredentials("p", pool)
	b := e.orderCredentials("p", pool)
	if a[0].ID == b[0].ID {
		t.Fatal("credential did not rotate")
	}
	e.markCredential("p", pool.Keys[1], domain.OutcomeSuccess, 0)
	pool.Policy = domain.CredentialSticky
	c := e.orderCredentials("p", pool)
	if c[0].ID != "b" {
		t.Fatalf("sticky got %s", c[0].ID)
	}
}
func TestRateLimitCooldownSkipsKey(t *testing.T) {
	e := newPolicyEngine()
	pool := domain.CredentialPool{Policy: domain.CredentialSequential, Keys: []domain.Credential{{ID: "a", Secret: "1", Enabled: true, Priority: 100}, {ID: "b", Secret: "2", Enabled: true, Priority: 90}}}
	e.markCredential("p", pool.Keys[0], domain.OutcomeRateLimited, 60)
	keys := e.orderCredentials("p", pool)
	if len(keys) != 1 || keys[0].ID != "b" {
		t.Fatalf("cooldown not applied: %#v", keys)
	}
	e.mu.Lock()
	e.cooldown["p/a"] = time.Now().Add(-time.Second)
	e.mu.Unlock()
	if len(e.orderCredentials("p", pool)) != 2 {
		t.Fatal("expired cooldown not restored")
	}
}
func TestFastestHealthyUsesStats(t *testing.T) {
	e := newPolicyEngine()
	ss := slots()
	e.stats[modelKey(ss[1])] = modelStat{Success: 3, AvgTTFT: 100}
	e.stats[modelKey(ss[0])] = modelStat{Success: 3, AvgTTFT: 900}
	got := e.orderModelsForPolicy("n", domain.PoolFastest, ss)
	if got[0].ID != "b" {
		t.Fatalf("fastest got %s", got[0].ID)
	}
}
