package desktop

import "testing"

func TestStudioLifecycleLazyStartCloseAndReopen(t *testing.T) {
	var s sessionLifecycle
	if s.running {
		t.Fatal("Studio must be unloaded initially")
	}
	start, show := s.requestOpen()
	if !start || show || !s.running {
		t.Fatal("first open must create a session")
	}
	start, show = s.requestOpen()
	if start || !show {
		t.Fatal("opening an existing Studio must only foreground it")
	}
	if !s.requestClose() || !s.closing {
		t.Fatal("close must begin session teardown")
	}
	start, show = s.requestOpen()
	if start || show || !s.reopen {
		t.Fatal("open during teardown must queue exactly one reopen")
	}
	if !s.sessionEnded() || !s.running || s.closing || s.reopen {
		t.Fatal("completed teardown must hand off to a fresh session")
	}
	if !s.requestClose() {
		t.Fatal("fresh session must be closable")
	}
	if s.sessionEnded() || s.running {
		t.Fatal("normal close must leave Studio fully unloaded")
	}
	if s.requestClose() {
		t.Fatal("closing an unloaded Studio must be a no-op")
	}
}
