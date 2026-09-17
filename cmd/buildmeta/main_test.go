package main

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{"1.2.3": "1.2.3.0", "2": "2.0.0.0", "1.2.3-beta": "1.2.3.0"}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Fatalf("%q => %q want %q", in, got, want)
		}
	}
}
