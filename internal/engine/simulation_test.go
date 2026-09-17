package engine

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func simConfig() domain.Config {
	c := config.Default()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Models = p.Models[:2]
	p.Order = domain.PoolPriority
	p.Dispatch = domain.DispatchSequential
	p.MaxCalls = 8
	p.MaxParallel = 2
	return c
}
func TestSimulationSequentialBudgetAndFallback(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	req := SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: p.Models[0].ModelID, Outcome: domain.OutcomeModelUnavailable, DurationMS: 300}}}
	res, err := Simulate(context.Background(), req, nil)
	if err != nil || res.Error != "" || res.Calls != 2 || res.ElapsedMS != 1300 || res.Trace.Winner.ModelID != p.Models[1].ModelID {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	c.Pipelines[0].Budget.MaxModelCalls = 1
	req.Config = c
	res, err = Simulate(context.Background(), req, nil)
	if err != nil || res.Calls != 1 || res.Error == "" {
		t.Fatalf("budget ignored: %+v %v", res, err)
	}
}
func TestSimulationStaggeredRaceUsesVirtualTimeAndCancelsLoser(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Dispatch = domain.DispatchStaggered
	p.HedgeDelayMS = 2000
	start := time.Now()
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: p.Models[0].ModelID, Outcome: domain.OutcomeSuccess, DurationMS: 9000}}}, nil)
	if err != nil || res.Error != "" || res.ElapsedMS != 3000 || res.Calls != 2 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("simulation used real delays")
	}
	found := false
	for _, ev := range res.Trace.Events {
		if ev.Kind == "attempt_end" && ev.Outcome == domain.OutcomeCancelled && ev.Message == "race_lost" {
			found = true
		}
	}
	if !found {
		t.Fatal("loser cancellation missing")
	}
}
func TestSimulationRetriesKeysAndRejectsAmbiguousOutcomes(t *testing.T) {
	c := simConfig()
	c.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "one", Enabled: true, Secret: "synthetic-one"}, {ID: "two", Enabled: true, Secret: "synthetic-two"}}
	c.Providers[0].CredentialPool.MaxKeysPerRequest = 2
	m := c.Pipelines[0].Nodes[1].ModelPool.Models[0]
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: m.ModelID, Attempt: 1, Outcome: domain.OutcomeAuthFailed, DurationMS: 100}}}, nil)
	if err != nil || res.Error != "" || res.Calls != 2 || res.Trace.Winner.CredentialID != "two" {
		t.Fatalf("key rotation %+v %v", res, err)
	}
	_, err = Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{Outcome: domain.OutcomeAnyError, DurationMS: 1}}}, nil)
	if err == nil {
		t.Fatal("any_error accepted as a concrete result")
	}
}
func TestSimulationNeverChangesOriginalStatisticsOrConfig(t *testing.T) {
	c := simConfig()
	c.RuntimeStatistics = &domain.RuntimeStatistics{Cursors: map[string]int{"model:" + c.ActivePipelineID + "/models": 1}}
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	for i := 0; i < 2; i++ {
		res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID}, nil)
		if err != nil || res.Trace.Winner.ModelID != p.Models[1].ModelID {
			t.Fatal(res, err)
		}
	}
	if c.RuntimeStatistics.Cursors["model:"+c.ActivePipelineID+"/models"] != 1 {
		t.Fatal("live cursor mutated")
	}
}

func TestSimulationSafetyCapIsExactAndRetainsConditionalError(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Models = nil
	p.MaxCalls = 1000
	c.Pipelines[0].Budget.MaxModelCalls = 1000
	for i := 0; i < 400; i++ {
		p.Models = append(p.Models, domain.ModelSlot{ID: fmt.Sprint(i), ProviderID: "opencode", ModelID: fmt.Sprint(i), Enabled: true, QualityScore: 100, MaxAttempts: 1})
	}
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{Outcome: domain.OutcomeModelUnavailable, DurationMS: 1}}}, nil)
	if err != nil || res.Calls != 256 || !strings.Contains(res.Error, "safety") {
		t.Fatalf("calls=%d error=%s (%v)", res.Calls, res.Error, err)
	}
}
