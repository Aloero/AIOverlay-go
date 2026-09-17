package config

import (
	"fmt"
	"strings"

	"aioverlay/internal/domain"
	"aioverlay/internal/hotkey"
	"aioverlay/internal/tunnel"
)

func Validate(cfg domain.Config) error {
	if cfg.Version != domain.ConfigVersion {
		return fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	if strings.TrimSpace(cfg.ActivePipelineID) == "" {
		return fmt.Errorf("active_pipeline_id is required")
	}
	providers := map[string]struct{}{}
	for i, p := range cfg.Providers {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("providers[%d].id is required", i)
		}
		if _, dup := providers[p.ID]; dup {
			return fmt.Errorf("duplicate provider id %q", p.ID)
		}
		providers[p.ID] = struct{}{}
		if err := validateProvider(p); err != nil {
			return err
		}
		if err := validateCredentials(p); err != nil {
			return err
		}
	}
	pipelineIDs := map[string]struct{}{}
	activeFound := false
	for i, p := range cfg.Pipelines {
		if _, dup := pipelineIDs[p.ID]; dup {
			return fmt.Errorf("duplicate pipeline id %q", p.ID)
		}
		pipelineIDs[p.ID] = struct{}{}
		if p.ID == cfg.ActivePipelineID {
			activeFound = p.Enabled
		}
		if err := validatePipeline(i, p, providers); err != nil {
			return err
		}
	}
	if !activeFound {
		return fmt.Errorf("active pipeline %q not found", cfg.ActivePipelineID)
	}
	if cfg.Overlay.Opacity < 0.05 || cfg.Overlay.Opacity > 1 {
		return fmt.Errorf("overlay.opacity must be between 0.05 and 1")
	}
	if cfg.Overlay.Width < 240 || cfg.Overlay.Height < 120 {
		return fmt.Errorf("overlay dimensions are too small")
	}
	if err := hotkey.Validate(cfg.Overlay.Hotkeys); err != nil {
		return err
	}
	if cfg.VPN.Enabled {
		engine := strings.TrimSpace(cfg.VPN.Engine)
		if engine != "" && engine != "sing-box" {
			return fmt.Errorf("vpn.engine must be sing-box")
		}
		if strings.TrimSpace(string(cfg.VPN.Config)) == "" {
			return fmt.Errorf("vpn.config is required when vpn.enabled is true")
		}
		if _, err := tunnel.BuildConfig(string(cfg.VPN.Config), 23456, "validation-user", "validation-pass"); err != nil {
			return fmt.Errorf("vpn.config: %w", err)
		}
	}
	if cfg.Build.ExecutableName == "" {
		return fmt.Errorf("build.executable_name is required")
	}
	if err := validateApplication(cfg); err != nil {
		return err
	}
	return nil
}

func validateCredentials(p domain.ProviderConfig) error {
	ids := map[string]struct{}{}
	secrets := map[string]bool{}
	for i, k := range p.CredentialPool.Keys {
		if strings.TrimSpace(k.ID) == "" {
			return fmt.Errorf("provider %s key[%d].id is required", p.ID, i)
		}
		if _, dup := ids[k.ID]; dup {
			return fmt.Errorf("provider %s has duplicate credential id %q", p.ID, k.ID)
		}
		if k.Secret != "" && secrets[k.Secret] {
			return fmt.Errorf("provider %s has duplicate credential secrets", p.ID)
		}
		secrets[k.Secret] = true
		ids[k.ID] = struct{}{}
	}
	return nil
}

func validatePipeline(index int, p domain.Pipeline, providers map[string]struct{}) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("pipelines[%d].id is required", index)
	}
	if p.Budget.MaxWallTimeMS <= 0 || p.Budget.MaxModelCalls <= 0 || p.Budget.MaxParallelCalls <= 0 {
		return fmt.Errorf("pipeline %s has invalid execution budget", p.ID)
	}
	if err := validateBudget(p.Budget); err != nil {
		return fmt.Errorf("pipeline %s: %w", p.ID, err)
	}
	nodes := map[string]domain.Node{}
	for _, n := range p.Nodes {
		if n.ID == "" {
			return fmt.Errorf("pipeline %s contains node without id", p.ID)
		}
		if _, dup := nodes[n.ID]; dup {
			return fmt.Errorf("pipeline %s duplicate node %q", p.ID, n.ID)
		}
		if err := validateNode(n); err != nil {
			return fmt.Errorf("pipeline %s node %s: %w", p.ID, n.ID, err)
		}
		nodes[n.ID] = n
		if n.Type == domain.NodeModelPool || n.Type == domain.NodeDecision {
			if n.ModelPool == nil {
				return fmt.Errorf("pipeline %s model pool node %s has no config", p.ID, n.ID)
			}
			if n.Type == domain.NodeDecision && n.ModelPool.Task == "transcribe" {
				return fmt.Errorf("pipeline %s decision node %s cannot use transcription task", p.ID, n.ID)
			}
			if err := validatePool(p.ID, n.ID, n.ModelPool, providers); err != nil {
				return err
			}
		}
	}
	if _, ok := nodes[p.EntryNodeID]; !ok {
		return fmt.Errorf("pipeline %s entry node %q not found", p.ID, p.EntryNodeID)
	}
	if _, ok := nodes[p.OutputNodeID]; !ok {
		return fmt.Errorf("pipeline %s output node %q not found", p.ID, p.OutputNodeID)
	}
	edges := map[string]struct{}{}
	for _, e := range p.Edges {
		if e.ID == "" {
			return fmt.Errorf("pipeline %s edge without id", p.ID)
		}
		if _, dup := edges[e.ID]; dup {
			return fmt.Errorf("pipeline %s duplicate edge %q", p.ID, e.ID)
		}
		if err := validateEdge(e); err != nil {
			return fmt.Errorf("pipeline %s edge %s: %w", p.ID, e.ID, err)
		}
		edges[e.ID] = struct{}{}
		if _, ok := nodes[e.From]; !ok {
			return fmt.Errorf("pipeline %s edge %s has unknown from %q", p.ID, e.ID, e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return fmt.Errorf("pipeline %s edge %s has unknown to %q", p.ID, e.ID, e.To)
		}
		if e.MaxVisits <= 0 {
			return fmt.Errorf("pipeline %s edge %s max_visits must be > 0", p.ID, e.ID)
		}
	}
	if nodes[p.OutputNodeID].Type != domain.NodeOutput {
		return fmt.Errorf("pipeline output node must have output type")
	}
	if err := validateSources(p, nodes); err != nil {
		return err
	}
	if !reachable(p) {
		return fmt.Errorf("pipeline %s output is unreachable", p.ID)
	}
	for _, c := range p.Commands {
		if !entryHasTerminal(p, nodes, c.EntryNodeID, c.Intent) {
			return fmt.Errorf("pipeline %s command %s has no valid terminal path", p.ID, c.ID)
		}
	}
	for _, src := range p.Sources {
		if !src.Enabled {
			continue
		}
		if !entryHasTerminal(p, nodes, src.EntryNodeID, src.Intent) {
			return fmt.Errorf("pipeline %s source %s has no valid terminal path", p.ID, src.ID)
		}
	}
	return nil
}

func validatePool(pipelineID, nodeID string, pool *domain.ModelPoolConfig, providers map[string]struct{}) error {
	if err := validatePoolLimits(pool); err != nil {
		return fmt.Errorf("pool %s: %w", nodeID, err)
	}
	if pool.MaxCalls <= 0 || pool.MaxParallel <= 0 {
		return fmt.Errorf("pipeline %s pool %s has invalid call limits", pipelineID, nodeID)
	}
	if len(pool.Models) == 0 && (pool.Selection == nil || pool.Selection.Mode == "manual" || pool.Selection.Mode == "") {
		return fmt.Errorf("pipeline %s pool %s contains no models", pipelineID, nodeID)
	}
	if pool.Selection != nil {
		s := pool.Selection
		if s.Mode != "manual" && s.Mode != "catalog" && s.Mode != "manual_then_catalog" {
			return fmt.Errorf("pool %s has invalid model source", nodeID)
		}
		if s.Modality != "auto" && s.Modality != "image" && s.Modality != "text" && s.Modality != "audio" && s.Modality != "audio_image" {
			return fmt.Errorf("pool %s has invalid catalog modality", nodeID)
		}
		if s.DefaultQualityScore < 0 || s.DefaultQualityScore > 100 {
			return fmt.Errorf("pool %s catalog quality must be 0..100", nodeID)
		}
		seenProviders := map[string]bool{}
		for _, id := range s.ProviderIDs {
			if _, ok := providers[id]; !ok {
				return fmt.Errorf("pool %s catalog references unknown provider %q", nodeID, id)
			}
			if seenProviders[id] {
				return fmt.Errorf("pool %s duplicate catalog provider %q", nodeID, id)
			}
			seenProviders[id] = true
		}
	}
	if pool.QualityFloor < 0 || pool.QualityFloor > 100 {
		return fmt.Errorf("pool %s quality floor must be 0..100", nodeID)
	}
	seen := map[string]struct{}{}
	for _, m := range pool.Models {
		if m.ID == "" {
			return fmt.Errorf("pipeline %s pool %s contains model slot without id", pipelineID, nodeID)
		}
		if _, dup := seen[m.ID]; dup {
			return fmt.Errorf("pipeline %s pool %s duplicate model slot %q", pipelineID, nodeID, m.ID)
		}
		if err := validateSlot(m); err != nil {
			return fmt.Errorf("pool %s: %w", nodeID, err)
		}
		seen[m.ID] = struct{}{}
		if _, ok := providers[m.ProviderID]; !ok {
			return fmt.Errorf("pipeline %s pool %s references unknown provider %q", pipelineID, nodeID, m.ProviderID)
		}
		if strings.TrimSpace(m.ModelID) == "" {
			return fmt.Errorf("pipeline %s pool %s model %s missing model_id", pipelineID, nodeID, m.ID)
		}
	}
	if len(nodeID) > 2048 {
		return fmt.Errorf("vision nesting too deep")
	}
	if pool.VisionPreprocessor != nil {
		if err := validatePool(pipelineID, nodeID+".vision_preprocessor", pool.VisionPreprocessor, providers); err != nil {
			return err
		}
	}
	return nil
}

func entryHasTerminal(p domain.Pipeline, nodes map[string]domain.Node, entry string, intent domain.CommandIntent) bool {
	type state struct {
		id                string
		decision, context bool
	}
	seen := map[state]bool{}
	queue := []state{{id: entry}}
	for len(queue) > 0 {
		st := queue[0]
		queue = queue[1:]
		if seen[st] {
			continue
		}
		seen[st] = true
		n, ok := nodes[st.id]
		if !ok {
			continue
		}
		st.decision = st.decision || n.Type == domain.NodeDecision
		st.context = st.context || n.Type == domain.NodeContext
		switch intent {
		case domain.IntentContext:
			if st.context {
				return true
			}
		case domain.IntentDecide:
			if st.decision && (n.Type == domain.NodeDecision || n.Type == domain.NodeOutput) {
				return true
			}
		default:
			if n.Type == domain.NodeOutput {
				return true
			}
		}
		for _, e := range p.Edges {
			if e.From == st.id && e.MaxVisits > 0 {
				next := st
				next.id = e.To
				queue = append(queue, next)
			}
		}
	}
	return false
}
