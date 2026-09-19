// JSON extension fields are part of the portable domain representation.
package domain

import (
	"encoding/json"
	"reflect"
	"strings"
)

func fieldNames(value any) map[string]bool {
	t := reflect.TypeOf(value)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}
func marshalExt(value any, extra map[string]json.RawMessage) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	known := fieldNames(value)
	for k, v := range extra {
		if !known[k] {
			obj[k] = v
		}
	}
	return json.Marshal(obj)
}
func unmarshalExt(raw []byte, value any) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(raw, value); err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	for k := range fieldNames(value) {
		delete(obj, k)
	}
	if len(obj) == 0 {
		return nil, nil
	}
	return obj, nil
}

func (v Config) MarshalJSON() ([]byte, error) {
	type plain Config
	return marshalExt(plain(v), v.Extra)
}
func (v *Config) UnmarshalJSON(raw []byte) error {
	type plain Config
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Config(p)
	v.Extra = extra
	return nil
}

func (v AppConfig) MarshalJSON() ([]byte, error) {
	type plain AppConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *AppConfig) UnmarshalJSON(raw []byte) error {
	type plain AppConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = AppConfig(p)
	v.Extra = extra
	return nil
}

func (v ProviderConfig) MarshalJSON() ([]byte, error) {
	type plain ProviderConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ProviderConfig) UnmarshalJSON(raw []byte) error {
	type plain ProviderConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ProviderConfig(p)
	v.Extra = extra
	return nil
}

func (v LocalSTTProviderConfig) MarshalJSON() ([]byte, error) {
	type plain LocalSTTProviderConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *LocalSTTProviderConfig) UnmarshalJSON(raw []byte) error {
	type plain LocalSTTProviderConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = LocalSTTProviderConfig(p)
	v.Extra = extra
	return nil
}

func (v RealtimeProviderConfig) MarshalJSON() ([]byte, error) {
	type plain RealtimeProviderConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *RealtimeProviderConfig) UnmarshalJSON(raw []byte) error {
	type plain RealtimeProviderConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = RealtimeProviderConfig(p)
	v.Extra = extra
	return nil
}

func (v RequestDefaults) MarshalJSON() ([]byte, error) {
	type plain RequestDefaults
	return marshalExt(plain(v), v.Extra)
}
func (v *RequestDefaults) UnmarshalJSON(raw []byte) error {
	type plain RequestDefaults
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = RequestDefaults(p)
	v.Extra = extra
	return nil
}

func (v CredentialPool) MarshalJSON() ([]byte, error) {
	type plain CredentialPool
	return marshalExt(plain(v), v.Extra)
}
func (v *CredentialPool) UnmarshalJSON(raw []byte) error {
	type plain CredentialPool
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = CredentialPool(p)
	v.Extra = extra
	return nil
}

func (v Credential) MarshalJSON() ([]byte, error) {
	type plain Credential
	return marshalExt(plain(v), v.Extra)
}
func (v *Credential) UnmarshalJSON(raw []byte) error {
	type plain Credential
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Credential(p)
	v.Extra = extra
	return nil
}

func (v Pipeline) MarshalJSON() ([]byte, error) {
	type plain Pipeline
	return marshalExt(plain(v), v.Extra)
}
func (v *Pipeline) UnmarshalJSON(raw []byte) error {
	type plain Pipeline
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Pipeline(p)
	v.Extra = extra
	return nil
}

func (v ExecutionBudget) MarshalJSON() ([]byte, error) {
	type plain ExecutionBudget
	return marshalExt(plain(v), v.Extra)
}
func (v *ExecutionBudget) UnmarshalJSON(raw []byte) error {
	type plain ExecutionBudget
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ExecutionBudget(p)
	v.Extra = extra
	return nil
}

func (v Node) MarshalJSON() ([]byte, error) { type plain Node; return marshalExt(plain(v), v.Extra) }
func (v *Node) UnmarshalJSON(raw []byte) error {
	type plain Node
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Node(p)
	v.Extra = extra
	return nil
}

func (v Position) MarshalJSON() ([]byte, error) {
	type plain Position
	return marshalExt(plain(v), v.Extra)
}
func (v *Position) UnmarshalJSON(raw []byte) error {
	type plain Position
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Position(p)
	v.Extra = extra
	return nil
}

func (v ModelPoolConfig) MarshalJSON() ([]byte, error) {
	type plain ModelPoolConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ModelPoolConfig) UnmarshalJSON(raw []byte) error {
	type plain ModelPoolConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ModelPoolConfig(p)
	v.Extra = extra
	return nil
}

func (v ModelSlot) MarshalJSON() ([]byte, error) {
	type plain ModelSlot
	return marshalExt(plain(v), v.Extra)
}
func (v *ModelSlot) UnmarshalJSON(raw []byte) error {
	type plain ModelSlot
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ModelSlot(p)
	v.Extra = extra
	return nil
}

func (v PromptConfig) MarshalJSON() ([]byte, error) {
	type plain PromptConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *PromptConfig) UnmarshalJSON(raw []byte) error {
	type plain PromptConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = PromptConfig(p)
	v.Extra = extra
	return nil
}

func (v ValidatorConfig) MarshalJSON() ([]byte, error) {
	type plain ValidatorConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ValidatorConfig) UnmarshalJSON(raw []byte) error {
	type plain ValidatorConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ValidatorConfig(p)
	v.Extra = extra
	return nil
}

func (v Edge) MarshalJSON() ([]byte, error) { type plain Edge; return marshalExt(plain(v), v.Extra) }
func (v *Edge) UnmarshalJSON(raw []byte) error {
	type plain Edge
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = Edge(p)
	v.Extra = extra
	return nil
}

func (v OverlayConfig) MarshalJSON() ([]byte, error) {
	type plain OverlayConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *OverlayConfig) UnmarshalJSON(raw []byte) error {
	type plain OverlayConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = OverlayConfig(p)
	v.Extra = extra
	return nil
}

func (v CanvasLayout) MarshalJSON() ([]byte, error) {
	type plain CanvasLayout
	return marshalExt(plain(v), v.Extra)
}
func (v *CanvasLayout) UnmarshalJSON(raw []byte) error {
	type plain CanvasLayout
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = CanvasLayout(p)
	v.Extra = extra
	return nil
}

func (v ControlConfig) MarshalJSON() ([]byte, error) {
	type plain ControlConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ControlConfig) UnmarshalJSON(raw []byte) error {
	type plain ControlConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ControlConfig(p)
	v.Extra = extra
	return nil
}

func (v VPNConfig) MarshalJSON() ([]byte, error) {
	type plain VPNConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *VPNConfig) UnmarshalJSON(raw []byte) error {
	type plain VPNConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = VPNConfig(p)
	v.Extra = extra
	return nil
}

func (v BuildProfile) MarshalJSON() ([]byte, error) {
	type plain BuildProfile
	return marshalExt(plain(v), v.Extra)
}
func (v *BuildProfile) UnmarshalJSON(raw []byte) error {
	type plain BuildProfile
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = BuildProfile(p)
	v.Extra = extra
	return nil
}

func (v CatalogSelection) MarshalJSON() ([]byte, error) {
	type plain CatalogSelection
	return marshalExt(plain(v), v.Extra)
}
func (v *CatalogSelection) UnmarshalJSON(raw []byte) error {
	type plain CatalogSelection
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = CatalogSelection(p)
	v.Extra = extra
	return nil
}

func (v RuntimeStatistics) MarshalJSON() ([]byte, error) {
	type plain RuntimeStatistics
	return marshalExt(plain(v), v.Extra)
}
func (v *RuntimeStatistics) UnmarshalJSON(raw []byte) error {
	type plain RuntimeStatistics
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = RuntimeStatistics(p)
	v.Extra = extra
	return nil
}

func (v ModelStatistics) MarshalJSON() ([]byte, error) {
	type plain ModelStatistics
	return marshalExt(plain(v), v.Extra)
}
func (v *ModelStatistics) UnmarshalJSON(raw []byte) error {
	type plain ModelStatistics
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ModelStatistics(p)
	v.Extra = extra
	return nil
}

func (v EventSource) MarshalJSON() ([]byte, error) {
	type plain EventSource
	return marshalExt(plain(v), v.Extra)
}
func (v *EventSource) UnmarshalJSON(raw []byte) error {
	type plain EventSource
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = EventSource(p)
	v.Extra = extra
	return nil
}

func (v ScreenRegionConfig) MarshalJSON() ([]byte, error) {
	type plain ScreenRegionConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ScreenRegionConfig) UnmarshalJSON(raw []byte) error {
	type plain ScreenRegionConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ScreenRegionConfig(p)
	v.Extra = extra
	return nil
}

func (v RealtimeSourceConfig) MarshalJSON() ([]byte, error) {
	type plain RealtimeSourceConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *RealtimeSourceConfig) UnmarshalJSON(raw []byte) error {
	type plain RealtimeSourceConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = RealtimeSourceConfig(p)
	v.Extra = extra
	return nil
}

func (v AudioSourceConfig) MarshalJSON() ([]byte, error) {
	type plain AudioSourceConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *AudioSourceConfig) UnmarshalJSON(raw []byte) error {
	type plain AudioSourceConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = AudioSourceConfig(p)
	v.Extra = extra
	return nil
}

func (v ConversationConfig) MarshalJSON() ([]byte, error) {
	type plain ConversationConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ConversationConfig) UnmarshalJSON(raw []byte) error {
	type plain ConversationConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ConversationConfig(p)
	v.Extra = extra
	return nil
}

func (v SessionBudget) MarshalJSON() ([]byte, error) {
	type plain SessionBudget
	return marshalExt(plain(v), v.Extra)
}
func (v *SessionBudget) UnmarshalJSON(raw []byte) error {
	type plain SessionBudget
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = SessionBudget(p)
	v.Extra = extra
	return nil
}

func (v CommandConfig) MarshalJSON() ([]byte, error) {
	type plain CommandConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *CommandConfig) UnmarshalJSON(raw []byte) error {
	type plain CommandConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = CommandConfig(p)
	v.Extra = extra
	return nil
}

func (v ContextConfig) MarshalJSON() ([]byte, error) {
	type plain ContextConfig
	return marshalExt(plain(v), v.Extra)
}
func (v *ContextConfig) UnmarshalJSON(raw []byte) error {
	type plain ContextConfig
	var p plain
	extra, err := unmarshalExt(raw, &p)
	if err != nil {
		return err
	}
	*v = ContextConfig(p)
	v.Extra = extra
	return nil
}
