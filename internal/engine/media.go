package engine

import (
	"aioverlay/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Observation struct {
	Kind        string
	Text        string
	SegmentID   string
	ContextID   string
	DerivedFrom []string
	Revision    uint64
	Final       bool
}
type decision struct {
	Action     string `json:"action"`
	Text       string `json:"text,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

func parseDecision(text string) (decision, error) {
	var d decision
	if len(text) > 64000 {
		return d, fmt.Errorf("decision response exceeds limit")
	}
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || !json.Valid([]byte(text)) {
		return d, fmt.Errorf("decision requires JSON with action, optional text and transcript")
	}
	switch d.Action {
	case "wait":
		d.Action = "no_action"
		return d, nil
	case "no_action", "respond", "need_screen":
		return d, nil
	}
	return d, fmt.Errorf("decision action must be no_action, respond or need_screen")
}
func (r *runner) observe(kind, text string) {
	r.observeValue(Observation{Kind: kind, Text: text})
}

func (r *runner) observeTranscript(text string) {
	segmentID := r.input.EventID
	if r.input.Audio != nil && r.input.Audio.SegmentID != "" {
		segmentID = r.input.Audio.SegmentID
	}
	r.observeValue(Observation{Kind: "audio.text", Text: text, SegmentID: segmentID, Revision: 1, Final: true})
}

func (r *runner) observeValue(observation Observation) {
	if observation.Text == "" {
		return
	}
	observation.Text = clipText(r.redact.text(observation.Text), 32000)
	observation.SegmentID = r.redact.text(observation.SegmentID)
	observation.ContextID = r.redact.text(observation.ContextID)
	for i := range observation.DerivedFrom {
		observation.DerivedFrom[i] = r.redact.text(observation.DerivedFrom[i])
	}
	r.observationMu.Lock()
	if len(r.observations) < 100 {
		r.observations = append(r.observations, observation)
	}
	r.observationMu.Unlock()
	if r.opt.OnObservation != nil {
		r.callbackMu.Lock()
		r.opt.OnObservation(observation)
		r.callbackMu.Unlock()
	}
}
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xc0) == 0x80 {
		n--
	}
	return s[:n]
}
func (r *runner) modelStage(ctx context.Context, n domain.Node, v nodeValue) (nodeValue, domain.Outcome, error) {
	if n.ModelPool != nil && n.ModelPool.ResultName == "context.summary" {
		contextID, derived := r.summaryProvenance(n)
		if contextID == "" || len(derived) == 0 {
			return v, domain.OutcomeValidationFailed, fmt.Errorf("context summary requires a selected context with provenance entries")
		}
	}
	if n.ModelPool != nil && n.ModelPool.Task == "transcribe" {
		ctx = context.WithValue(ctx, silentKey{}, true)
	}
	if r.input.Intent == "context" {
		ctx = context.WithValue(ctx, silentKey{}, true)
	}
	out, o, err := r.runPool(ctx, n, v)
	if err != nil {
		return out, o, err
	}
	if n.ModelPool.Task == "transcribe" || n.ModelPool.ResultName == "audio.text" {
		out.transcript = out.text
		out.audio = nil
		out.vision = v.vision
		r.observeTranscript(out.text)
	}
	if n.ModelPool.ResultName == "vision.text" {
		out.vision = out.text
		r.observe("vision.text", out.text)
	}
	if n.ModelPool.ResultName == "context.summary" {
		contextID, derived := r.summaryProvenance(n)
		r.observeValue(Observation{Kind: "context.summary", Text: out.text, ContextID: contextID, DerivedFrom: derived, SegmentID: n.ID + ":" + r.input.EventID, Revision: 1, Final: true})
		out.hasOutput = false
	}
	return out, o, nil
}
func (r *runner) decide(ctx context.Context, n domain.Node, v nodeValue) (nodeValue, domain.Outcome, error) {
	if r.input.Intent == "respond" {
		r.emit(domain.TraceEvent{Kind: "decision", NodeID: n.ID, Outcome: domain.OutcomeRespond, Message: "Manual respond intent bypasses only the decision gate"})
		return v, domain.OutcomeRespond, nil
	}
	if r.input.Intent == "context" {
		return v, domain.OutcomeContextOnly, nil
	}
	raw, outcome, err := r.runPool(context.WithValue(ctx, silentKey{}, true), n, v)
	if err != nil {
		return v, outcome, err
	}
	d, err := parseDecision(raw.text)
	if err != nil {
		return v, domain.OutcomeInvalidResponse, err
	}
	if d.Transcript != "" {
		v.transcript = d.Transcript
		r.observeTranscript(d.Transcript)
	}
	o := domain.Outcome(d.Action)
	if o == domain.OutcomeRespond {
		if d.Text != "" {
			v.text = d.Text
			v.hasOutput = true
		} else {
			v.text = ""
			v.hasOutput = false
		}
	}
	r.emit(domain.TraceEvent{Kind: "decision", NodeID: n.ID, Outcome: o, Message: "Structured model decision"})
	return v, o, nil
}
func (r *runner) capture(ctx context.Context, node string) ([]byte, error) {
	if r.opt.Capture == nil {
		return nil, fmt.Errorf("screen action is not available in this execution")
	}
	if r.captures >= 3 {
		return nil, fmt.Errorf("screen action limit reached (3 per turn)")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.captures++
	r.emit(domain.TraceEvent{Kind: "capture_start", NodeID: node, Message: "Pipeline requested a fresh screen; RAM only"})
	image, err := r.opt.Capture(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && len(image) == 0 {
		err = fmt.Errorf("screen action returned an empty image")
	}
	if err != nil {
		return nil, err
	}
	return image, nil
}

func (r *runner) summaryProvenance(node domain.Node) (string, []string) {
	contextID := r.input.DefaultContextID
	mode := node.ContextMode
	if mode == "" && node.ContextID != "" {
		mode = "named"
	}
	switch mode {
	case "none":
		return "", nil
	case "named":
		contextID = node.ContextID
	}
	view, ok := r.input.ContextViews[contextID]
	if !ok {
		return contextID, nil
	}
	return contextID, append([]string(nil), view.EntryIDs...)
}
