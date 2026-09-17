package engine

import (
	"context"
	"sync"

	"aioverlay/internal/domain"
)

type streamKey struct{}
type silentKey struct{}

// Only one provisional response can paint the UI. A different completed winner
// replaces it explicitly instead of concatenating unrelated model responses.
type streamGate struct {
	mu     sync.Mutex
	runner *runner
	owner  string
	text   string
	closed bool
	silent bool
}

func (g *streamGate) push(id, delta string) {
	if g == nil || g.silent || delta == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	if g.owner == "" {
		g.owner = id
	}
	if g.owner != id {
		return
	}
	g.text += delta
	g.runner.callbackMu.Lock()
	defer g.runner.callbackMu.Unlock()
	if g.runner.opt.OnDelta != nil {
		g.runner.opt.OnDelta(delta)
	}
}
func (g *streamGate) release(id string) {
	if g == nil || g.silent {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.owner != id {
		return
	}
	g.owner = ""
	g.text = ""
	g.runner.callbackMu.Lock()
	defer g.runner.callbackMu.Unlock()
	if g.runner.opt.OnReset != nil {
		g.runner.opt.OnReset()
	}
}
func (r *runner) commit(ctx context.Context, n domain.Node, m domain.ModelSlot, text string) {
	g, _ := ctx.Value(streamKey{}).(*streamGate)
	if g == nil || g.silent {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	if g.text != text {
		r.callbackMu.Lock()
		if r.opt.OnReset != nil {
			r.opt.OnReset()
		}
		if r.opt.OnDelta != nil {
			r.opt.OnDelta(text)
		}
		r.callbackMu.Unlock()
	}
	r.traceMu.Lock()
	r.trace.Winner = r.successes[n.ID+"/"+m.ID]
	winner := r.trace.Winner
	r.traceMu.Unlock()
	r.emit(domain.TraceEvent{Kind: "winner", NodeID: n.ID, Attempt: winner, Outcome: domain.OutcomeSuccess, Message: "Accepted answer; competing attempts cancelled."})
}
