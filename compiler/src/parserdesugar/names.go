package parserdesugar

func AppendMissingNames(dst []string, names []string) []string {
	for _, n := range names {
		found := false
		for _, d := range dst {
			if d == n {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, n)
		}
	}
	return dst
}

func appendMissingNames(dst, names []string) []string { return AppendMissingNames(dst, names) }
