package engine

// A bounded, virtual-time rehearsal. It shares candidate selection, ordering,
// limits, admission and graph-edge selection with execution. It creates no
// provider registry, timers, screenshots or network clients. Outcomes are supplied
// by the user, not predictions; live health/cursors are never changed.
import (
	"context"
	"fmt"
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
)

type SimulationOutcome struct {
	NodeID       string         `json:"node_id"`
	ProviderID   string         `json:"provider_id"`
	ModelID      string         `json:"model_id"`
	Attempt      int            `json:"attempt,omitempty"`
	Outcome      domain.Outcome `json:"outcome"`
	DurationMS   int            `json:"duration_ms"`
	FirstTokenMS int            `json:"first_token_ms"`
	Text         string         `json:"text,omitempty"`
}
type SimulationRequest struct {
	Config             domain.Config       `json:"config"`
	PipelineID         string              `json:"pipeline_id"`
	TriggerID          string              `json:"trigger_id,omitempty"`
	ContextText        string              `json:"context_text,omitempty"`
	Outcomes           []SimulationOutcome `json:"outcomes"`
	InputText          string              `json:"input_text,omitempty"`
	InitialCredentials []CredentialState   `json:"-"`
}
type SimulationReport struct {
	Simulated bool                  `json:"simulated"`
	Trace     domain.ExecutionTrace `json:"trace"`
	Calls     int                   `json:"calls"`
	ElapsedMS int64                 `json:"elapsed_ms"`
	Error     string                `json:"error,omitempty"`
	Text      string                `json:"text,omitempty"`
	Remaining int                   `json:"remaining_candidates"`
}
type simulator struct {
	ctx           context.Context
	r             *runner
	req           SimulationRequest
	now           int64
	until         map[string]int64
	modelAttempts map[string]int
	report        SimulationReport
	steps         int
	planID        string
	halted        bool
}
type simChain struct {
	model                        domain.ModelSlot
	provider                     domain.ProviderConfig
	keys                         []domain.Credential
	keyIndex, repeats, keysTried int
	maxKeys                      int
	ref                          *domain.AttemptRef
	start, finish, first         int64
	outcome                      domain.Outcome
	text                         string
	emittedFirst                 bool
}

func Simulate(ctx context.Context, req SimulationRequest, cat *catalog.Service) (SimulationReport, error) {
	if len(req.Outcomes) > 128 || len(req.InputText) > 8192 {
		return SimulationReport{}, fmt.Errorf("simulation input is too large")
	}
	for _, o := range req.Outcomes {
		if o.DurationMS < 1 || o.DurationMS > 600000 || o.FirstTokenMS < 0 || o.FirstTokenMS > 600000 || o.Attempt < 0 || o.Attempt > 256 || len(o.Text) > 8192 || !simulationOutcome(o.Outcome) {
			return SimulationReport{}, fmt.Errorf("invalid conditional outcome")
		}
	}
	p, ok := findPipeline(req.Config, req.PipelineID)
	if !ok {
		return SimulationReport{}, fmt.Errorf("simulation pipeline missing or disabled")
	}
	if req.TriggerID != "" {
		if source, found := simulationSource(&p, req.TriggerID); found && source.Realtime != nil {
			return simulateRealtimeSource(req, p, source), nil
		}
	}
	// Safety is separate from the real budget and reported explicitly.
	if p.Budget.MaxParallelCalls < 1 || p.Budget.MaxParallelCalls > 64 || p.Budget.MaxModelCalls < 1 || p.Budget.MaxWallTimeMS < 1 {
		return SimulationReport{}, fmt.Errorf("invalid simulation budget")
	}
	e := New(req.Config, nil, cat, nil)
	e.ImportStatistics(req.Config.RuntimeStatistics)
	tr := domain.ExecutionTrace{ID: "simulation", PipelineID: p.ID, PipelineName: p.Name, Budget: p.Budget, Purpose: "simulation", StartedAt: time.Unix(0, 0)}
	s := &simulator{ctx: ctx, req: req, until: map[string]int64{}, modelAttempts: map[string]int{}, report: SimulationReport{Simulated: true}}
	input, value, nodeID, err := simulationInput(&p, req)
	if err != nil {
		return SimulationReport{}, err
	}
	tr.SourceID = input.SourceID
	tr.Intent = input.Intent
	s.r = newRunner(e, p, input, RunOptions{}, &tr)
	tr.PipelineName = s.r.redact.text(tr.PipelineName)
	tr.Budget.Extra = nil
	for _, key := range req.InitialCredentials {
		if until := time.Until(key.CooldownUntil).Milliseconds(); until > 0 {
			s.until[key.ProviderID+"/"+key.CredentialID] = until
		}
	}
	for {
		if err := s.check(); err != nil {
			s.report.Error = err.Error()
			break
		}
		n, ok := findNode(p, nodeID)
		if !ok {
			return SimulationReport{}, fmt.Errorf("unknown simulation node")
		}
		s.r.lastNode = n.ID
		step, stepErr := s.executeNode(n, value)
		if stepErr != nil {
			return SimulationReport{}, stepErr
		}
		out, outcome, err := step.value, step.outcome, step.err
		if step.output {
			s.r.lastOutcome = outcome
			s.report.Trace = tr
			s.finish()
			return s.report, nil
		}
		s.r.lastOutcome = outcome
		s.emit(domain.TraceEvent{Kind: "node_end", NodeID: n.ID, Outcome: outcome})
		if step.terminal {
			break
		}
		edge, ok := s.r.nextEdge(n.ID, outcome)
		if !ok {
			if err != nil {
				s.report.Error = err.Error()
			} else {
				s.report.Error = "No transition for the conditional outcome"
			}
			break
		}
		s.emit(domain.TraceEvent{Kind: "transition", NodeID: n.ID, EdgeID: edge.ID, Outcome: outcome, Message: edge.To})
		s.r.edgeVisits[edge.ID]++
		s.now += int64(edge.DelayMS)
		nodeID = edge.To
		if err == nil {
			value = out
		}
	}
	s.report.Trace = tr
	s.finish()
	return s.report, nil
}

func simulateRealtimeSource(request SimulationRequest, pipeline domain.Pipeline, source domain.EventSource) SimulationReport {
	trace := domain.ExecutionTrace{
		ID: "simulation", PipelineID: pipeline.ID, PipelineName: pipeline.Name,
		Budget: pipeline.Budget, Purpose: "simulation", SourceID: source.ID,
		Intent: string(source.Intent), StartedAt: time.Unix(0, 0),
	}
	report := SimulationReport{Simulated: true, Trace: trace}
	stageID := "realtime:" + source.ID
	trace.Events = append(trace.Events, domain.TraceEvent{
		Kind: "realtime_append", NodeID: stageID, Outcome: domain.OutcomeSuccess,
		Message: "Synthetic audio appended to persistent remote session; no socket was opened",
	})
	if source.Realtime.ResponsePolicy == "manual" {
		trace.Outcome = domain.OutcomeContextOnly
		trace.FinishedAt = trace.StartedAt
		report.Trace = trace
		report.Remaining = len(request.Outcomes)
		return report
	}
	var selected *SimulationOutcome
	for index := range request.Outcomes {
		outcome := &request.Outcomes[index]
		if outcome.NodeID == stageID && (outcome.ProviderID == "" || outcome.ProviderID == source.Realtime.ProviderID) && (outcome.ModelID == "" || outcome.ModelID == source.Realtime.ModelID) {
			selected = outcome
			break
		}
	}
	if selected == nil {
		report.Error = "Missing simulated delegated response outcome for realtime source"
		trace.Outcome = domain.OutcomeInvalidResponse
		trace.FinishedAt = trace.StartedAt
		report.Trace = trace
		report.Remaining = len(request.Outcomes)
		return report
	}
	report.Calls = 1
	report.ElapsedMS = int64(selected.DurationMS)
	report.Remaining = max(0, len(request.Outcomes)-1)
	attempt := &domain.AttemptRef{ID: "simulation-realtime", ProviderID: source.Realtime.ProviderID, ModelID: source.Realtime.ModelID, Transport: "OpenAI Live"}
	trace.Events = append(trace.Events,
		domain.TraceEvent{Kind: "attempt_start", NodeID: stageID, Attempt: attempt, AtMS: 0},
		domain.TraceEvent{Kind: "attempt_end", NodeID: stageID, Attempt: attempt, AtMS: int64(selected.DurationMS), Outcome: selected.Outcome},
	)
	trace.Outcome = selected.Outcome
	trace.FinishedAt = trace.StartedAt.Add(time.Duration(selected.DurationMS) * time.Millisecond)
	if selected.Outcome == domain.OutcomeSuccess || selected.Outcome == domain.OutcomeRespond {
		report.Text = selected.Text
		trace.Outcome = domain.OutcomeRespond
		trace.Winner = attempt
	} else {
		report.Error = "Simulated realtime response outcome: " + string(selected.Outcome)
	}
	report.Trace = trace
	return report
}

func (s *simulator) finish() {
	s.report.Calls = int(s.r.calls.Load())
	s.report.ElapsedMS = s.now
	s.report.Trace.FinishedAt = s.report.Trace.StartedAt.Add(time.Duration(s.now) * time.Millisecond)
	s.report.Error = s.r.redact.text(s.report.Error)
	s.report.Text = s.r.redact.text(s.report.Text)
	for _, event := range poolCompletionEvents(s.report.Trace.Events) {
		event.AtMS = s.now
		s.report.Trace.Events = append(s.report.Trace.Events, event)
	}
	var err error
	if s.report.Error != "" {
		err = fmt.Errorf("%s", s.report.Error)
	}
	finishTrace(&s.report.Trace, err, context.Cause(s.ctx), s.r.lastNode, s.r.lastOutcome, s.r.redact)
	if err == nil && s.r.waited {
		if s.r.input.Intent == string(domain.IntentContext) {
			s.report.Trace.Outcome = domain.OutcomeContextOnly
		} else {
			s.report.Trace.Outcome = domain.OutcomeNoAction
		}
		s.report.Trace.Winner = nil
	}
	s.report.Trace = domain.CloneTrace(s.report.Trace)
}
func simulationOutcome(o domain.Outcome) bool {
	switch o {
	case domain.OutcomeSuccess, domain.OutcomeAuthFailed, domain.OutcomeRateLimited, domain.OutcomeQuotaExceeded, domain.OutcomeTimeout, domain.OutcomeFirstTokenTimeout, domain.OutcomeStreamIdleTimeout, domain.OutcomeNetworkError, domain.OutcomeModelUnavailable, domain.OutcomeRegionRestricted, domain.OutcomeProviderUnavailable, domain.OutcomeUnsupportedModality, domain.OutcomeInvalidResponse, domain.OutcomeValidationFailed, domain.OutcomeCancelled:
		return true
	}
	return false
}
func (s *simulator) check() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.steps++
	if s.steps > 10000 || s.r.calls.Load() > 256 {
		return fmt.Errorf("simulation safety limit reached (256 calls / 10000 steps)")
	}
	if s.now >= int64(s.r.pipeline.Budget.MaxWallTimeMS) {
		s.now = int64(s.r.pipeline.Budget.MaxWallTimeMS)
		return fmt.Errorf("max wall time reached")
	}
	return nil
}
func (s *simulator) emit(ev domain.TraceEvent) {
	ev.AtMS = s.now
	ev.PlanID = s.planID
	ev = s.r.redact.event(ev)
	s.r.trace.Events = append(s.r.trace.Events, domain.CloneTraceEvent(ev))
}
