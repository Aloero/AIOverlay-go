package domain

import "encoding/json"

// Ordering is independent of dispatch. Legacy Policy remains a compatibility
// input for programmatic callers; schema 3 materializes both fields on import.
type DispatchPolicy string

const (
	DispatchSequential DispatchPolicy = "sequential"
	DispatchStaggered  DispatchPolicy = "staggered_race"
	DispatchParallel   DispatchPolicy = "parallel_race"
)

func (p ModelPoolConfig) EffectiveOrder() PoolPolicy {
	if p.Order != "" {
		return p.Order
	}
	switch p.Policy {
	case PoolRoundRobin, PoolWeighted, PoolFastest:
		return p.Policy
	default:
		return PoolPriority
	}
}
func (p ModelPoolConfig) EffectiveDispatch() DispatchPolicy {
	if p.Dispatch != "" {
		return p.Dispatch
	}
	switch p.Policy {
	case PoolParallel:
		return DispatchParallel
	case PoolStaggered:
		return DispatchStaggered
	default:
		return DispatchSequential
	}
}

type WorkspaceConfig struct {
	Extra            map[string]json.RawMessage `json:"-"`
	SidebarCollapsed bool                       `json:"sidebar_collapsed,omitempty"`
	ResultCollapsed  bool                       `json:"result_collapsed,omitempty"`
	InspectorWidth   int                        `json:"inspector_width,omitempty"`
	ShowFallbacks    bool                       `json:"show_fallbacks,omitempty"`
}

func (v WorkspaceConfig) MarshalJSON() ([]byte, error) {
	type plain WorkspaceConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *WorkspaceConfig) UnmarshalJSON(raw []byte) error {
	type plain WorkspaceConfig
	var p plain
	e, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = WorkspaceConfig(p)
	v.Extra = e
	return nil
}
