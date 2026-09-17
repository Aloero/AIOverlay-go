package config

import (
	"aioverlay/internal/domain"
	"bytes"
	"encoding/json"
	"testing"
)

func TestSchemaTwoSplitsAllLegacySchedules(t *testing.T) {
	for _, policy := range []domain.PoolPolicy{domain.PoolRoundRobin, domain.PoolPriority, domain.PoolWeighted, domain.PoolFastest, domain.PoolParallel, domain.PoolStaggered} {
		c := Default()
		c.Version = 2
		p := c.Pipelines[0].Nodes[1].ModelPool
		p.Policy = policy
		order, dispatch := p.EffectiveOrder(), p.EffectiveDispatch()
		p.VisionPreprocessor = &domain.ModelPoolConfig{Policy: policy}
		if err := Migrate(&c); err != nil {
			t.Fatal(err)
		}
		if p.Order != order || p.Dispatch != dispatch || p.VisionPreprocessor.Order != order || c.Version != domain.ConfigVersion {
			t.Fatalf("migration changed %s", policy)
		}
	}
}
func TestIndependentScheduleRoundTripAndValidation(t *testing.T) {
	c := Default()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	p.Dispatch = domain.DispatchStaggered
	b, _ := json.Marshal(c)
	out, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if out.Pipelines[0].Nodes[1].ModelPool.Dispatch != p.Dispatch {
		t.Fatal("lost dispatch")
	}
	p.Order = "parallel_race"
	if Validate(c) == nil {
		t.Fatal("dispatch used as ordering")
	}
	p.Order = domain.PoolPriority
	p.Dispatch = "weighted"
	if Validate(c) == nil {
		t.Fatal("ordering used as dispatch")
	}
}
