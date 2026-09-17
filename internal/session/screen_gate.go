package session

import (
	"aioverlay/internal/domain"
	"crypto/sha256"
	"time"
)

type screenChangeGate struct {
	changedOnly bool
	debounce    time.Duration
	maxWait     time.Duration

	published    [32]byte
	candidate    [32]byte
	hasPublished bool
	hasCandidate bool
	candidateAt  time.Time
	changeAt     time.Time
}

func newScreenChangeGate(source domain.EventSource) screenChangeGate {
	return screenChangeGate{
		changedOnly: source.ChangedOnly,
		debounce:    time.Duration(source.DebounceMS) * time.Millisecond,
		maxWait:     time.Duration(source.MaxWaitMS) * time.Millisecond,
	}
}

func (g *screenChangeGate) Accept(image []byte, capturedAt time.Time) bool {
	if !g.changedOnly {
		return true
	}

	hash := sha256.Sum256(image)
	if !g.hasPublished {
		g.publish(hash)
		return true
	}
	if hash == g.published {
		g.clearPendingChange()
		return false
	}
	if g.debounce <= 0 {
		g.publish(hash)
		return true
	}
	if g.changeAt.IsZero() {
		g.changeAt = capturedAt
	}
	if g.maxWait > 0 && !capturedAt.Before(g.changeAt.Add(g.maxWait)) {
		g.publish(hash)
		return true
	}
	if !g.hasCandidate || hash != g.candidate {
		g.candidate = hash
		g.candidateAt = capturedAt
		g.hasCandidate = true
		return false
	}
	if capturedAt.Before(g.candidateAt.Add(g.debounce)) {
		return false
	}

	g.publish(hash)
	return true
}

func (g *screenChangeGate) publish(hash [32]byte) {
	g.published = hash
	g.hasPublished = true
	g.clearPendingChange()
}

func (g *screenChangeGate) clearPendingChange() {
	g.candidate = [32]byte{}
	g.hasCandidate = false
	g.candidateAt = time.Time{}
	g.changeAt = time.Time{}
}
