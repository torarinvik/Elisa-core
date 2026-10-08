package ast

import "strings"

// PermissionRefSurfaceList formats source-level grants, grouping only adjacent
// members with identical families and type arguments. Via clauses and family
// grants remain individual. The callback retains each caller's type spelling.
func PermissionRefSurfaceList(refs []PermissionRef, format func(PermissionRef) string) string {
	parts := make([]string, 0, len(refs))
	for i := 0; i < len(refs); {
		ref := refs[i]
		if ref.Member == "" || len(ref.Via) != 0 {
			parts = append(parts, format(ref))
			i++
			continue
		}
		base := ref
		base.Member = ""
		family := format(base)
		members := []string{ref.Member}
		j := i + 1
		for j < len(refs) {
			next := refs[j]
			if next.Member == "" || len(next.Via) != 0 {
				break
			}
			nextBase := next
			nextBase.Member = ""
			if format(nextBase) != family {
				break
			}
			members = append(members, next.Member)
			j++
		}
		if len(members) > 1 {
			parts = append(parts, family+"{"+strings.Join(members, ",")+"}")
		} else {
			parts = append(parts, format(ref))
		}
		i = j
	}
	return strings.Join(parts, ", ")
}
