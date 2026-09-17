package buildprofile

import "strings"

func NormalizeVersion(v string) string {
	parts := strings.Split(strings.TrimSpace(v), ".")
	clean := make([]string, 0, 4)
	for _, p := range parts {
		var digits strings.Builder
		for _, r := range p {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			} else {
				break
			}
		}
		if digits.Len() == 0 {
			clean = append(clean, "0")
		} else {
			clean = append(clean, digits.String())
		}
		if len(clean) == 4 {
			break
		}
	}
	for len(clean) < 4 {
		clean = append(clean, "0")
	}
	return strings.Join(clean, ".")
}
