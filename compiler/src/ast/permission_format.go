package ast

import "strings"

// PermissionRefSurfaceList formats source-level grants, grouping members with
// identical families and type arguments even when another family appears
// between them. Group order follows each family's first appearance; member
// order follows the input. Via clauses and family grants remain individual.
// The callback retains each caller's type spelling.
func PermissionRefSurfaceList(refs []PermissionRef, format func(PermissionRef) string) string {
	parts := make([]string, 0, len(refs))
	consumed := make([]bool, len(refs))
	for i := 0; i < len(refs); i++ {
		if consumed[i] {
			continue
		}
		ref := refs[i]
		if ref.Member == "" || len(ref.Via) != 0 {
			parts = append(parts, format(ref))
			continue
		}
		base := ref
		base.Member = ""
		family := format(base)
		members := []string{ref.Member}
		consumed[i] = true
		for j := i + 1; j < len(refs); j++ {
			next := refs[j]
			if consumed[j] {
				continue
			}
			if next.Member == "" || len(next.Via) != 0 {
				continue
			}
			nextBase := next
			nextBase.Member = ""
			if format(nextBase) != family {
				continue
			}
			members = append(members, next.Member)
			consumed[j] = true
		}
		if len(members) > 1 {
			parts = append(parts, family+"{"+strings.Join(members, ",")+"}")
		} else {
			parts = append(parts, format(ref))
		}
	}
	return strings.Join(parts, ", ")
}
