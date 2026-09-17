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
	// Safety is separate from the real budget and reported explicitly.
	if p.Budget.MaxParallelCalls < 1 || p.Budget.MaxParallelCalls > 64 || p.Budget.MaxModelCalls < 1 || p.Budget.MaxWallTimeMS < 1 {
		return SimulationReport{}, fmt.Errorf("invalid simulation budget")
	}
	e := New(req.Config, nil, cat, nil)
	e.ImportStatistics(req.Config.RuntimeStatistics)
	tr := domain.ExecutionTrace{ID: "simulation", PipelineID: p.ID, PipelineName: p.Name, Budget: p.Budget, Purpose: "simulation", StartedAt: time.Unix(0, 0)}
	s := &simulator{ctx: ctx, req: req, until: map[string]int64{}, modelAttempts: map[string]int{}, report: SimulationReport{Simulated: true}}
	s.r = newRunner(e, p, Input{Text: req.InputText, ImageJPEG: []byte{1}}, RunOptions{}, &tr)
	tr.PipelineName = s.r.redact.text(tr.PipelineName)
	tr.Budget.Extra = nil
	for _, key := range req.InitialCredentials {
		if until := time.Until(key.CooldownUntil).Milliseconds(); until > 0 {
			s.until[key.ProviderID+"/"+key.CredentialID] = until
		}
	}
	value := nodeValue{image: []byte{1}, text: req.InputText}
	nodeID := p.EntryNodeID
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
		outcome := domain.OutcomeSuccess
		out := value
		var err error
		switch n.Type {
		case domain.NodeScreen:
		case domain.NodeModelPool:
			out, outcome, err = s.pool(n, value)
		case domain.NodeValidator:
			outcome, err = runValidator(n.Validator, value.text)
		case domain.NodeOutput:
			s.report.Text = value.text
			s.report.Trace = tr
			s.finish()
			return s.report, nil
		default:
			return SimulationReport{}, fmt.Errorf("unsupported simulation node")
		}
		s.r.lastOutcome = outcome
		s.emit(domain.TraceEvent{Kind: "node_end", NodeID: n.ID, Outcome: outcome})
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
