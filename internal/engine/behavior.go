package engine

import (
	"fmt"
	"sort"

	"aioverlay/internal/domain"
)

type BehaviorNode struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Type domain.NodeType `json:"type"`
}

type BehaviorAIStage struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        domain.NodeType `json:"type"`
	Task        string          `json:"task,omitempty"`
	ResultName  string          `json:"result_name,omitempty"`
	ContextMode string          `json:"context_mode,omitempty"`
	ContextID   string          `json:"context_id,omitempty"`
	ContextName string          `json:"context_name,omitempty"`
	Input       PoolInput       `json:"input"`
}

type BehaviorScreenPolicy struct {
	CaptureIntervalMS int                        `json:"capture_interval_ms"`
	ContextIntervalMS int                        `json:"context_interval_ms"`
	TriggerIntervalMS int                        `json:"trigger_interval_ms"`
	ChangedOnly       bool                       `json:"changed_only"`
	DebounceMS        int                        `json:"debounce_ms"`
	MaxWaitMS         int                        `json:"max_wait_ms"`
	Region            *domain.ScreenRegionConfig `json:"region,omitempty"`
}

type BehaviorTrigger struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Kind        string                `json:"kind"`
	Capture     string                `json:"capture"`
	Intent      domain.CommandIntent  `json:"intent"`
	ContextID   string                `json:"context_id,omitempty"`
	ContextName string                `json:"context_name,omitempty"`
	ContextRole string                `json:"context_role,omitempty"`
	EntryNodeID string                `json:"entry_node_id"`
	AutoStart   bool                  `json:"auto_start,omitempty"`
	UsesAI      bool                  `json:"uses_ai"`
	Reachable   []BehaviorNode        `json:"reachable"`
	AIStages    []BehaviorAIStage     `json:"ai_stages,omitempty"`
	Screen      *BehaviorScreenPolicy `json:"screen,omitempty"`
}

type BehaviorContext struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IncludeTranscript bool   `json:"include_transcript"`
	IncludeAnswers    bool   `json:"include_answers"`
	KeepLatestScreen  bool   `json:"keep_latest_screen"`
	MaxAgeS           int    `json:"max_age_s"`
	MaxEntries        int    `json:"max_entries"`
	MaxChars          int    `json:"max_chars"`
}

type BehaviorReport struct {
	PipelineID string                `json:"pipeline_id"`
	Commands   []BehaviorTrigger     `json:"commands"`
	Sources    []BehaviorTrigger     `json:"sources"`
	Contexts   []BehaviorContext     `json:"contexts"`
	Budget     *domain.SessionBudget `json:"session_budget,omitempty"`
	Warnings   []string              `json:"warnings,omitempty"`
}

func DescribePipelineBehavior(pipeline domain.Pipeline) BehaviorReport {
	report := BehaviorReport{PipelineID: pipeline.ID, Budget: cloneSessionBudget(pipeline.SessionBudget)}
	contexts := enabledBehaviorContexts(pipeline.Contexts)

	for _, command := range pipeline.Commands {
		trigger := behaviorCommand(pipeline, command, contexts)
		report.Commands = append(report.Commands, trigger)
		report.warnMissingEntry(trigger)
	}
	for _, source := range pipeline.Sources {
		if !source.Enabled {
			continue
		}
		trigger := behaviorSource(pipeline, source, contexts)
		report.Sources = append(report.Sources, trigger)
		report.warnMissingEntry(trigger)
	}
	for _, context := range pipeline.Contexts {
		if !context.Enabled {
			continue
		}
		report.Contexts = append(report.Contexts, BehaviorContext{
			ID: context.ID, Name: behaviorName(context.Name, context.ID),
			IncludeTranscript: context.IncludeTranscript, IncludeAnswers: context.IncludeAnswers,
			KeepLatestScreen: context.KeepLatestScreen, MaxAgeS: context.MaxAgeS,
			MaxEntries: context.MaxEntries, MaxChars: context.MaxChars,
		})
	}
	return report
}

func (report *BehaviorReport) warnMissingEntry(trigger BehaviorTrigger) {
	if len(trigger.Reachable) == 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s %s references an unavailable entry", trigger.Kind, trigger.ID))
	}
}

func behaviorCommand(pipeline domain.Pipeline, command domain.CommandConfig, contexts map[string]string) BehaviorTrigger {
	skipRun := command.Intent == domain.IntentContext
	start := commandInputState(pipeline, command)
	trigger := BehaviorTrigger{
		ID: command.ID, Name: behaviorName(command.Name, command.ID), Kind: "command",
		Intent: command.Intent, ContextID: command.ContextID, ContextName: contexts[command.ContextID],
		EntryNodeID: command.EntryNodeID,
		Reachable:   reachableBehaviorNodes(pipeline, command.EntryNodeID, command.Intent, skipRun),
	}
	if command.CaptureScreen {
		trigger.Capture = "screen"
	}
	if command.ContextID != "" {
		if command.Intent == domain.IntentContext {
			trigger.ContextRole = "write"
		} else {
			trigger.ContextRole = "read"
		}
	}
	if !skipRun {
		trigger.AIStages = behaviorAIStages(pipeline, trigger.Reachable, []inputState{start}, command.Intent)
	}
	trigger.UsesAI = len(trigger.AIStages) > 0
	return trigger
}

func behaviorSource(pipeline domain.Pipeline, source domain.EventSource, contexts map[string]string) BehaviorTrigger {
	skipRun := source.Kind == domain.SourceScreenTimer && source.Intent == domain.IntentContext
	start := sourceInputState(pipeline, source)
	trigger := BehaviorTrigger{
		ID: source.ID, Name: behaviorName(source.Name, source.ID), Kind: "source",
		Intent: source.Intent, ContextID: source.ContextID, ContextName: contexts[source.ContextID],
		ContextRole: behaviorSourceContextRole(source), EntryNodeID: source.EntryNodeID,
		AutoStart: source.AutoStart,
		Reachable: reachableBehaviorNodes(pipeline, source.EntryNodeID, source.Intent, skipRun),
	}
	trigger.Capture = behaviorSourceCapture(source)
	if source.Kind == domain.SourceScreenTimer {
		trigger.Screen = behaviorScreenPolicy(source)
	}
	if !skipRun {
		trigger.AIStages = behaviorAIStages(pipeline, trigger.Reachable, []inputState{start}, source.Intent)
	}
	trigger.UsesAI = len(trigger.AIStages) > 0
	return trigger
}

func behaviorScreenPolicy(source domain.EventSource) *BehaviorScreenPolicy {
	capture := max(1000, source.IntervalMS)
	contextInterval := source.ContextIntervalMS
	if contextInterval < capture {
		contextInterval = capture
	}
	triggerInterval := source.TriggerIntervalMS
	if triggerInterval < capture {
		triggerInterval = capture
	}
	var region *domain.ScreenRegionConfig
	if source.ScreenRegion != nil {
		copy := *source.ScreenRegion
		copy.Extra = nil
		region = &copy
	}
	return &BehaviorScreenPolicy{
		CaptureIntervalMS: capture, ContextIntervalMS: contextInterval, TriggerIntervalMS: triggerInterval,
		ChangedOnly: source.ChangedOnly, DebounceMS: source.DebounceMS, MaxWaitMS: source.MaxWaitMS, Region: region,
	}
}

func behaviorSourceCapture(source domain.EventSource) string {
	switch source.Kind {
	case domain.SourceAudio:
		if source.Audio != nil && source.Audio.Mode == "microphone" {
			return "microphone"
		}
		return "system_audio"
	case domain.SourceScreenTimer:
		return "screen_periodic"
	default:
		return string(source.Kind)
	}
}

func behaviorSourceContextRole(source domain.EventSource) string {
	if source.ContextID == "" {
		return ""
	}
	if source.Intent == domain.IntentContext {
		return "write"
	}
	return "read_write"
}

func reachableBehaviorNodes(pipeline domain.Pipeline, entryID string, intent domain.CommandIntent, skipRun bool) []BehaviorNode {
	if entryID == "" {
		return nil
	}
	nodes := behaviorNodeMap(pipeline)
	if _, ok := nodes[entryID]; !ok {
		return nil
	}
	if skipRun {
		node := nodes[entryID]
		return []BehaviorNode{{ID: node.ID, Name: behaviorName(node.Name, node.ID), Type: node.Type}}
	}

	outgoing := behaviorOutgoingEdges(pipeline)
	queue := []string{entryID}
	seen := map[string]bool{}
	result := make([]BehaviorNode, 0, len(nodes))
	for len(queue) > 0 && len(result) < 64 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		node, ok := nodes[id]
		if !ok {
			continue
		}
		result = append(result, BehaviorNode{ID: node.ID, Name: behaviorName(node.Name, node.ID), Type: node.Type})
		if behaviorRouteStops(node, intent) {
			continue
		}
		for _, edge := range outgoing[id] {
			if behaviorEdgeAllowed(node, edge, intent) && !seen[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}
	return result
}

func behaviorNodeMap(pipeline domain.Pipeline) map[string]domain.Node {
	nodes := make(map[string]domain.Node, len(pipeline.Nodes))
	for _, node := range pipeline.Nodes {
		nodes[node.ID] = node
	}
	return nodes
}

func behaviorOutgoingEdges(pipeline domain.Pipeline) map[string][]domain.Edge {
	outgoing := map[string][]domain.Edge{}
	for _, edge := range pipeline.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	for id := range outgoing {
		sort.SliceStable(outgoing[id], func(i, j int) bool {
			if outgoing[id][i].Priority != outgoing[id][j].Priority {
				return outgoing[id][i].Priority > outgoing[id][j].Priority
			}
			return outgoing[id][i].ID < outgoing[id][j].ID
		})
	}
	return outgoing
}

func behaviorRouteStops(node domain.Node, intent domain.CommandIntent) bool {
	return intent == domain.IntentContext && node.Type == domain.NodeContext
}

func behaviorEdgeAllowed(node domain.Node, edge domain.Edge, intent domain.CommandIntent) bool {
	if node.Type != domain.NodeDecision || intent != domain.IntentRespond {
		return true
	}
	for _, outcome := range edge.On {
		if outcome == domain.OutcomeRespond {
			return true
		}
	}
	return false
}

func behaviorAIStages(pipeline domain.Pipeline, reachable []BehaviorNode, starts []inputState, intent domain.CommandIntent) []BehaviorAIStage {
	nodes := behaviorNodeMap(pipeline)
	var stages []BehaviorAIStage
	for _, reachableNode := range reachable {
		node := nodes[reachableNode.ID]
		if node.ModelPool == nil || node.Type != domain.NodeModelPool && node.Type != domain.NodeDecision {
			continue
		}
		if node.Type == domain.NodeDecision && intent == domain.IntentRespond {
			continue
		}
		if node.ModelPool.VisionPreprocessor != nil {
			stages = append(stages, behaviorAIStage(pipeline, node, node.ID+"/vision", node.Name+" / Vision", node.ModelPool.VisionPreprocessor, starts))
		}
		stages = append(stages, behaviorAIStage(pipeline, node, node.ID, behaviorName(node.Name, node.ID), node.ModelPool, starts))
	}
	return stages
}

func behaviorAIStage(pipeline domain.Pipeline, node domain.Node, id, name string, pool *domain.ModelPoolConfig, starts []inputState) BehaviorAIStage {
	return BehaviorAIStage{
		ID: id, Name: behaviorName(name, id), Type: node.Type,
		Task: pool.Task, ResultName: pool.ResultName,
		ContextMode: behaviorNodeContextMode(node), ContextID: node.ContextID, ContextName: behaviorContextName(pipeline, node.ContextID),
		Input: describePoolInput(pipeline, id, starts),
	}
}

func enabledBehaviorContexts(contexts []domain.ContextConfig) map[string]string {
	result := map[string]string{}
	for _, context := range contexts {
		if context.Enabled {
			result[context.ID] = behaviorName(context.Name, context.ID)
		}
	}
	return result
}

func behaviorName(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

func cloneSessionBudget(budget *domain.SessionBudget) *domain.SessionBudget {
	if budget == nil {
		return nil
	}
	copy := *budget
	copy.Extra = nil
	return &copy
}

func behaviorNodeContextMode(node domain.Node) string {
	if node.ContextMode != "" {
		return node.ContextMode
	}
	if node.ContextID != "" {
		return "named"
	}
	return "trigger"
}

func behaviorContextName(pipeline domain.Pipeline, id string) string {
	for _, context := range pipeline.Contexts {
		if context.ID == id {
			return behaviorName(context.Name, context.ID)
		}
	}
	return ""
}
