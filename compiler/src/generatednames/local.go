package generatednames

import "strings"

func IsLocalGeneratedName(name string) bool {
	const (
		autoPrefix       = "__auto_"
		quantifierPrefix = "__quantifier_"
	)
	var suffix string
	switch {
	case strings.HasPrefix(name, autoPrefix):
		suffix = name[len(autoPrefix):]
	case strings.HasPrefix(name, quantifierPrefix):
		suffix = name[len(quantifierPrefix):]
	default:
		return false
	}
	if len(suffix) == 0 {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	return true
}
