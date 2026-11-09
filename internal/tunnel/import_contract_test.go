package tunnel

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type importContractCase struct {
	Input string          `json:"input"`
	Want  ImportedProfile `json:"want"`
	Error string          `json:"error,omitempty"`
}

// Fixtures were captured before splitting parsing, protocol, TLS and transport.
// They test the local conversion contract, not connectivity to a VPN endpoint.
func TestShareProfileCompatibilityFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/import_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []importContractCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Input, func(t *testing.T) {
			got, err := ImportProfile(tc.Input)
			message := ""
			if err != nil {
				message = err.Error()
			}
			got.Config = compactContractJSON(t, got.Config)
			tc.Want.Config = compactContractJSON(t, tc.Want.Config)
			if message != tc.Error || !reflect.DeepEqual(got, tc.Want) {
				t.Fatalf("got %+v error=%q; want %+v error=%q", got, message, tc.Want, tc.Error)
			}
		})
	}
}

func compactContractJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(compact.Bytes(), []byte("null")) {
		return nil
	}
	return compact.Bytes()
}
