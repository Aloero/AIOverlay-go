package control

import (
	"aioverlay/internal/session"
	"testing"
	"time"
)

func TestPresentationArbiterRejectsAutoTriggeredBeforeManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Running: true}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 1, Text: "old auto", TriggeredAt: start.Add(-time.Second)}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("background answer replaced a running manual answer")
	}

	job.Running = false
	job.Finished = start.Add(time.Second)
	arbiter.ObserveJob(job)
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("rejected background answer was reconsidered after manual completion")
	}
}

func TestPresentationArbiterRejectsAutoTriggeredDuringManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Finished: start.Add(2 * time.Second)}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 1, Text: "late callback", TriggeredAt: start.Add(time.Second)}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("auto answer triggered during manual ownership was published")
	}
}

func TestPresentationArbiterAcceptsNewerAutoAfterManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Finished: start.Add(time.Second)}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 7, Text: "fresh auto", TriggeredAt: start.Add(2 * time.Second)}
	if !arbiter.AcceptBackground(job, auto) {
		t.Fatal("fresh background answer after manual completion was suppressed")
	}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("same background publication was accepted twice")
	}
}
