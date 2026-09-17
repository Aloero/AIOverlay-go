package platform

import "time"

type ProtectionStatus struct {
	Requested        bool      `json:"requested"`
	Applied          bool      `json:"applied"`
	Mode             string    `json:"mode"`
	Error            string    `json:"error,omitempty"`
	CompositionError string    `json:"composition_error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}
