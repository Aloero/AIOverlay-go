package control

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestControlContractJSONRemainsCompatible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"catalog_query", CatalogQuery{}, `{"provider_id":"","search":"","provider":"","vision":false,"audio":false,"free":false,"reasoning":false,"ready":false,"limit":0,"offset":0}`},
		{"catalog_page", CatalogPage{}, `{"items":null,"total":0,"offset":0,"limit":0}`},
		{"test_request", TestRequest{}, `{"pipeline_id":"","image_base64":"","text":""}`},
		{"benchmark_request", BenchmarkRequest{}, `{"pipeline_id":"","runs":0}`},
		{"benchmark_run", BenchmarkRun{}, `{"verdict":"","ttft_ms":0,"tps":0,"run":0,"duration_ms":0,"correct":false,"text":""}`},
		{"benchmark_report", BenchmarkReport{}, `{"requested":0,"pipeline_id":"","successful":0,"unverified":0,"success_rate":0,"cancelled":false,"check_method":"","runs":null,"median_ms":0,"correct":0,"total":0}`},
		{"studio_state", StudioState{}, `{"recent_models":null,"active_pipeline_id":"","revision":0,"credentials":null,"version":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("contract changed:\n got %s\nwant %s", raw, tc.want)
			}
		})
	}
}
