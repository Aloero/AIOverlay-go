package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"aioverlay/internal/domain"
)

var ErrBudgetExhausted = errors.New("execution budget exhausted")

type BudgetError struct{ Scope string }

func (e *BudgetError) Error() string {
	name := map[string]string{"model_calls": "max model calls", "pool_calls": "pool model calls", "keys": "provider key limit", "providers": "max providers"}[e.Scope]
	return "execution budget exhausted: " + name
}
func (e *BudgetError) Unwrap() error { return ErrBudgetExhausted }
func terminalPoolBudget(err error) bool {
	var b *BudgetError
	return errors.As(err, &b) && (b.Scope == "model_calls" || b.Scope == "pool_calls")
}

type nodeValue struct {
	image     []byte
	text      string
	vision    string
	hasOutput bool
}
type runner struct {
	lastNode      string
	lastOutcome   domain.Outcome
	redact        redactor
	traceMu       sync.Mutex
	callbackMu    sync.Mutex
	workers       sync.WaitGroup
	successes     map[string]*domain.AttemptRef
	nodeCalls     map[string]int
	keysUsed      map[string]map[string]bool
	engine        *Engine
	pipeline      domain.Pipeline
	input         Input
	opt           RunOptions
	trace         *domain.ExecutionTrace
	started       time.Time
	calls         atomic.Int32
	providersMu   sync.Mutex
	providersUsed map[string]struct{}
	edgeVisits    map[string]int
	sem           chan struct{}
}

func newRunner(e *Engine, p domain.Pipeline, input Input, opt RunOptions, trace *domain.ExecutionTrace) *runner {
	return &runner{
		engine: e, pipeline: p, input: input, opt: opt, trace: trace, redact: redactor{e.secrets()},
		started: time.Now(), successes: map[string]*domain.AttemptRef{}, nodeCalls: map[string]int{}, keysUsed: map[string]map[string]bool{}, providersUsed: map[string]struct{}{}, edgeVisits: map[string]int{},
		sem: make(chan struct{}, max(1, p.Budget.MaxParallelCalls)),
	}
}

func (r *runner) emit(ev domain.TraceEvent) {
	ev.AtMS = time.Since(r.started).Milliseconds()
	ev = r.redact.event(ev)
	r.traceMu.Lock()
	r.trace.Events = append(r.trace.Events, domain.CloneTraceEvent(ev))
	r.traceMu.Unlock()
	if r.opt.OnTrace != nil {
		r.callbackMu.Lock()
		r.opt.OnTrace(domain.CloneTraceEvent(ev))
		r.callbackMu.Unlock()
	}
}

func (r *runner) admit(n domain.Node, pool domain.ModelPoolConfig, m domain.ModelSlot, key domain.Credential) error {
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	if r.calls.Load() >= int32(r.pipeline.Budget.MaxModelCalls) {
		return &BudgetError{Scope: "model_calls"}
	}
	if pool.MaxCalls > 0 && r.nodeCalls[n.ID] >= pool.MaxCalls {
		return &BudgetError{Scope: "pool_calls"}
	}
	keys := r.keysUsed[m.ProviderID]
	if keys == nil {
		keys = map[string]bool{}
		r.keysUsed[m.ProviderID] = keys
	}
	if !keys[key.ID] && r.pipeline.Budget.MaxKeysPerProvider > 0 && len(keys) >= r.pipeline.Budget.MaxKeysPerProvider {
		return &BudgetError{Scope: "keys"}
	}
	if _, used := r.providersUsed[m.ProviderID]; !used && r.pipeline.Budget.MaxProviders > 0 && len(r.providersUsed) >= r.pipeline.Budget.MaxProviders {
		return &BudgetError{Scope: "providers"}
	}
	r.providersUsed[m.ProviderID] = struct{}{}
	keys[key.ID] = true
	r.calls.Add(1)
	r.nodeCalls[n.ID]++
	return nil
}

func (r *runner) run(ctx context.Context, nodeID string, value nodeValue) (string, error) {

	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, ok := findNode(r.pipeline, nodeID)
		if !ok {
			return "", fmt.Errorf("node %q not found", nodeID)
		}
		r.lastNode = n.ID
		var out nodeValue
		outcome := domain.OutcomeSuccess
		var err error
		switch n.Type {
		case domain.NodeScreen:
			out = value
			if len(out.image) == 0 {
				outcome = domain.OutcomeInvalidResponse
				err = fmt.Errorf("screen image is empty")
			}
		case domain.NodeModelPool:
			out, outcome, err = r.runPool(ctx, n, value)
		case domain.NodeValidator:
			out = value
			outcome, err = runValidator(n.Validator, value.text)
		case domain.NodeOutput:
			return value.text, nil
		default:
			outcome = domain.OutcomeInvalidResponse
			err = fmt.Errorf("unsupported node type %s", n.Type)
		}
		r.lastOutcome = outcome
		r.emit(domain.TraceEvent{Kind: "node_end", NodeID: n.ID, Outcome: outcome})
		next, ok := r.nextEdge(n.ID, outcome)
		if !ok {
			if err != nil {
				return "", err
			}
			if n.ID == r.pipeline.OutputNodeID {
				return out.text, nil
			}
			return "", fmt.Errorf("node %s has no edge for outcome %s", n.ID, outcome)
		}
		r.emit(domain.TraceEvent{Kind: "transition", NodeID: n.ID, EdgeID: next.ID, Outcome: outcome, Message: next.To})
		r.edgeVisits[next.ID]++
		if r.edgeVisits[next.ID] > next.MaxVisits {
			return "", fmt.Errorf("edge %s max_visits exceeded", next.ID)
		}
		if next.DelayMS > 0 {
			select {
			case <-time.After(time.Duration(next.DelayMS) * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		nodeID = next.To
		if err == nil {
			value = out
		} // Fallback keeps the failed node's original input.
	}
}

func (r *runner) nextEdge(from string, outcome domain.Outcome) (domain.Edge, bool) {
	var list []domain.Edge
	for _, ed := range r.pipeline.Edges {
		if ed.From != from || r.edgeVisits[ed.ID] >= ed.MaxVisits {
			continue
		}
		for _, o := range ed.On {
			if o == outcome || o == domain.OutcomeAnyError && outcome != domain.OutcomeSuccess {
				list = append(list, ed)
				break
			}
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Priority > list[j].Priority })
	if len(list) == 0 {
		return domain.Edge{}, false
	}
	return list[0], true
}
