package benchmark

import "testing"

func TestFixtureGradingIsNotSubstringSearch(t *testing.T) {
	for answer, want := range map[string]string{"Q4, 210": "correct", "Q4 has the highest profit: 210.": "correct", "**Q4 — 210**": "correct", "Q1 210": "incorrect", "Q4 120": "incorrect", "Not Q4, not 210. The answer is Q1": "unverified", "Q1 120; Q2 170; Q3 155; Q4 210": "unverified", `{"quarter":"Q4","profit":210}`: "correct"} {
		if got := Evaluate(answer); got != want {
			t.Fatalf("%q: %s, want %s", answer, got, want)
		}
	}
}
func TestEvenMedianAndEmpty(t *testing.T) {
	if Median([]int64{4, 1, 2, 9}) != 3 || Median(nil) != 0 || Median([]int64{10, 2, 6}) != 6 {
		t.Fatal("wrong median")
	}
}
