package benchmark

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Evaluate checks only an unambiguous fixture answer. A paragraph mentioning both
// Q4 and 210 is not automatically correct. Unrecognized prose stays unverified.
func Evaluate(answer string) string {
	text := strings.TrimSpace(strings.Trim(answer, "`"))
	var object struct {
		Quarter string `json:"quarter"`
		Profit  int    `json:"profit"`
	}
	if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &object) == nil && object.Quarter != "" {
		if strings.EqualFold(object.Quarter, "Q4") && object.Profit == 210 {
			return "correct"
		}
		return "incorrect"
	}
	text = strings.NewReplacer("**", "", "__", "", "—", "-", "–", "-").Replace(text)
	pattern := regexp.MustCompile(`(?i)^\s*(?:answer\s*:\s*)?(?:quarter\s+)?Q([1-4])\s*(?:(?:has\s+the\s+highest\s+profit|profit|прибыль)\s*)?(?:is\s*)?[:;,=\-\s]*(\d+)\s*\.?\s*$`)
	m := pattern.FindStringSubmatch(text)
	if m == nil {
		return "unverified"
	}
	value, _ := strconv.Atoi(m[2])
	if m[1] == "4" && value == 210 {
		return "correct"
	}
	return "incorrect"
}
func Median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	copy := append([]int64(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	middle := len(copy) / 2
	if len(copy)%2 == 1 {
		return copy[middle]
	}
	return copy[middle-1] + (copy[middle]-copy[middle-1])/2
}
