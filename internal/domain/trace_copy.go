package domain

func CloneTraceEvent(e TraceEvent) TraceEvent {
	if e.Attempt != nil {
		a := *e.Attempt
		e.Attempt = &a
	}
	return e
}
func CloneTrace(t ExecutionTrace) ExecutionTrace {
	if t.Winner != nil {
		a := *t.Winner
		t.Winner = &a
	}
	events := make([]TraceEvent, len(t.Events))
	for i, e := range t.Events {
		events[i] = CloneTraceEvent(e)
	}
	t.Events = events
	return t
}
