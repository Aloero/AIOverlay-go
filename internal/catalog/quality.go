package catalog

import "strings"

func HeuristicQuality(modelID string) float64 {
	name := strings.ToLower(modelID)
	score := 64.0
	for marker, v := range map[string]float64{
		"gemini-3.1-pro": 98, "gemini-3.8-flash": 93, "gemini-3.7-flash": 90,
		"mimo-v2.5": 90, "muse-spark-1.3": 84, "muse-spark-1.2": 82,
		// Free OpenCode Zen models added when the allow-list was refreshed (2026-11-10).
		// "minimax-m3.1" scores above its raw tier because the "mini" penalty below applies.
		"nemotron-3.5-super-vl": 88, "minimax-m3.1": 93, "longcat-2.5": 84, "mimo-v2.6": 85,
		"inkling-2-small": 78, "nemotron-3.5-lightning": 80, "flint-40b": 72, "beam-free": 70, "iceman-free": 70,
		"qwen3.6": 90, "kimi-k2.5": 90, "glm-5": 90, "deepseek-v4": 94,
		"qwen3-vl": 89, "qwen2.5-vl-72b": 83, "llama-3.2-90b-vision": 82,
	} {
		if strings.Contains(name, marker) && v > score {
			score = v
		}
	}
	if strings.Contains(name, "reasoning") || strings.Contains(name, "thinking") {
		score += 3
	}
	if strings.Contains(name, "mini") {
		score -= 7
	}
	if strings.Contains(name, "nano") || strings.Contains(name, "lite") {
		score -= 11
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
