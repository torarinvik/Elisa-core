package semantic

import (
	"elisacore/src/ast"
	"sort"
)

func permissionMembersMatch(existing *PermissionSet, members []string) bool {
	if existing == nil {
		return false
	}
	if len(existing.MemberSet) != len(members) {
		return false
	}
	for _, member := range members {
		if !existing.MemberSet[member] {
			return false
		}
	}
	return true
}

func permissionRefKey(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}

func canonicalizePermissionRefs(refs []ast.PermissionRef) []ast.PermissionRef {
	if len(refs) == 0 {
		return nil
	}
	familyRefs := make(map[string]ast.PermissionRef)
	memberRefs := make(map[string]ast.PermissionRef)
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		if ref.Member == "" {
			familyRefs[ref.Name] = ast.PermissionRef{Position: ref.Position, Name: ref.Name}
			for key, existing := range memberRefs {
				if existing.Name == ref.Name {
					delete(memberRefs, key)
				}
			}
			continue
		}
		if _, ok := familyRefs[ref.Name]; ok {
			continue
		}
		memberRefs[permissionRefKey(ref)] = ast.PermissionRef{Position: ref.Position, Name: ref.Name, Member: ref.Member}
	}
	out := make([]ast.PermissionRef, 0, len(familyRefs)+len(memberRefs))
	for _, ref := range familyRefs {
		out = append(out, ref)
	}
	for _, ref := range memberRefs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Member < out[j].Member
	})
	return out
}

func permissionFamiliesFromRefs(refs []ast.PermissionRef) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		out = append(out, ref.Name)
	}
	sort.Strings(out)
	return out
}

func (a *Analyzer) grantedPermissionRefs(refs []ast.PermissionRef) map[string]bool {
	granted := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		granted[permissionRefKey(ref)] = true
		if ref.Member == "" {
			a.markSubsumedFamilies(ref.Name, granted)
		}
	}
	return granted
}

// markSubsumedFamilies adds, into granted, every family transitively reachable
// from `family` through `includes` declarations. A whole-family grant of `Y`
// where `Y: includes X` therefore also grants `X` (and all of X's members). The
// `granted` map doubles as the visited set, so include cycles terminate.
func (a *Analyzer) markSubsumedFamilies(family string, granted map[string]bool) {
	ps := a.permissions[family]
	if ps == nil {
		return
	}
	for _, inc := range ps.Includes {
		if granted[inc] {
			continue
		}
		granted[inc] = true
		a.markSubsumedFamilies(inc, granted)
	}
}

// permissionAnyName is the reserved top (⊤) permission: a `can[any]` grant
// satisfies every requirement (the explicit erasure escape for FFI, stored
// heterogeneous closures, and dynamic dispatch). A `can[any]` *requirement*,
// conversely, is only satisfied by another `any` grant (or a `trusted` block) —
// it does not fall out of any concrete grant.
const permissionAnyName = "any"

func permissionRefGranted(ref ast.PermissionRef, granted map[string]bool) bool {
	if ref.Name == "" {
		return true
	}
	if granted[permissionAnyName] {
		return true
	}
	if granted[ref.Name] {
		return true
	}
	if ref.Member != "" && granted[permissionRefKey(ref)] {
		return true
	}
	return false
}

func missingGrantedPermissionRefs(refs []ast.PermissionRef, granted map[string]bool) []ast.PermissionRef {
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 0 {
		return nil
	}
	missing := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if permissionRefGranted(ref, granted) {
			continue
		}
		missing = append(missing, ref)
	}
	return canonicalizePermissionRefs(missing)
}

func mergePermissionFamilies(left []string, right []string) []string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(left)+len(right))
	for _, family := range left {
		if seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	for _, family := range right {
		if seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func mergePermissionRefs(left []ast.PermissionRef, right []ast.PermissionRef) []ast.PermissionRef {
	merged := make([]ast.PermissionRef, 0, len(left)+len(right))
	merged = append(merged, left...)
	merged = append(merged, right...)
	return canonicalizePermissionRefs(merged)
}

func functionPermissionRefs(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	if len(fnType.PermissionRefs) != 0 {
		return fnType.PermissionRefs
	}
	refs := make([]ast.PermissionRef, 0, len(fnType.Permissions))
	for _, family := range fnType.Permissions {
		refs = append(refs, ast.PermissionRef{Name: family})
	}
	return refs
}

func filterPermissionRefsByFamilies(refs []ast.PermissionRef, families []string) []ast.PermissionRef {
	if len(refs) == 0 || len(families) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(families))
	for _, family := range families {
		allowed[family] = true
	}
	out := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if allowed[ref.Name] {
			out = append(out, ref)
		}
	}
	return canonicalizePermissionRefs(out)
}

func permissionGrantHint(refs []ast.PermissionRef, families []string) string {
	refs = filterPermissionRefsByFamilies(refs, families)
	if len(refs) == 0 {
		for _, family := range families {
			refs = append(refs, ast.PermissionRef{Name: family})
		}
	}
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 1 && refs[0].Member != "" {
		return "can " + PermissionRefString(refs[0])
	}
	return PermissionRefsString(refs)
}

func (a *Analyzer) resolvePermissionFamilies(refs []ast.PermissionRef, report bool) []string {
	return permissionFamiliesFromRefs(a.resolvePermissionRefs(refs, report))
}

func (a *Analyzer) resolvePermissionRefs(refs []ast.PermissionRef, report bool) []ast.PermissionRef {
	if len(refs) == 0 {
		return nil
	}
	valid := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == permissionAnyName {
			if ref.Member != "" {
				if report {
					a.errorf(ref.Position, "the top permission %q does not support member access", permissionAnyName)
				}
				continue
			}
			valid = append(valid, ast.PermissionRef{Position: ref.Position, Name: permissionAnyName})
			continue
		}
		if a.lookupPermissionParam(ref.Name) {
			if ref.Member != "" {
				if report {
					a.errorf(ref.Position, "permission parameter %q does not support member access", ref.Name)
				}
				continue
			}
			valid = append(valid, ast.PermissionRef{Position: ref.Position, Name: ref.Name})
			continue
		}
		permission, _, ok := a.lookupVisiblePermission(ref.Name)
		if !ok {
			if report {
				a.errorf(ref.Position, "%s", UnknownPermissionMessage(ref.Name))
			}
			continue
		}
		if ref.Member != "" && !permission.MemberSet[ref.Member] {
			if report {
				a.errorf(ref.Position, "permission %q has no member %q", ref.Name, ref.Member)
			}
			continue
		}
		valid = append(valid, ref)
	}
	return canonicalizePermissionRefs(valid)
}

func (a *Analyzer) recordFunctionPermissionFamilies(families []string) {
	if len(families) == 0 || a.currentFunctionUsedPermissions == nil {
		return
	}
	for _, family := range families {
		a.currentFunctionUsedPermissions[family] = true
	}
}

func (a *Analyzer) recordFunctionPermissionRefs(refs []ast.PermissionRef) {
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 0 {
		return
	}
	a.currentFunctionUsedPermissionRefs = append(a.currentFunctionUsedPermissionRefs, refs...)
	a.recordFunctionPermissionFamilies(permissionFamiliesFromRefs(refs))
}

func sortedPermissionFamilies(families map[string]bool) []string {
	if len(families) == 0 {
		return nil
	}
	out := make([]string, 0, len(families))
	for family := range families {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func funcTypeUsesPermissionRowParam(fnType *FuncType) bool {
	return fnType != nil && len(fnType.PermissionParams) != 0
}

func (a *Analyzer) grantedIncludesPermissionParam(granted map[string]bool) bool {
	if a != nil && a.currentFuncType != nil && len(a.currentFuncType.PermissionParams) != 0 {
		return true
	}
	if a == nil || len(granted) == 0 {
		return false
	}
	for name := range granted {
		if a.lookupPermissionParam(name) {
			return true
		}
	}
	return false
}

// permissionWarningsSuppressedByGenericContext reports whether permission-effect
// warnings should stay quiet because the current contract is intentionally
// abstract over permission families (for example `can[P]`).
func (a *Analyzer) permissionWarningsSuppressedByGenericContext(fnType *FuncType, granted map[string]bool) bool {
	return funcTypeUsesPermissionRowParam(fnType) || a.grantedIncludesPermissionParam(granted)
}

func cloneGrantedPermissionFamilies(granted map[string]bool) map[string]bool {
	if len(granted) == 0 {
		return map[string]bool{}
	}
	cloned := make(map[string]bool, len(granted))
	for family := range granted {
		cloned[family] = true
	}
	return cloned
}

func extendGrantedPermissionFamilies(granted map[string]bool, families []string) map[string]bool {
	next := cloneGrantedPermissionFamilies(granted)
	for _, family := range families {
		next[family] = true
	}
	return next
}

func (a *Analyzer) extendGrantedPermissionRefs(granted map[string]bool, refs []ast.PermissionRef) map[string]bool {
	next := cloneGrantedPermissionFamilies(granted)
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		next[permissionRefKey(ref)] = true
		if ref.Member == "" {
			a.markSubsumedFamilies(ref.Name, next)
		}
	}
	return next
}

func samePermissionRefs(left []ast.PermissionRef, right []ast.PermissionRef) bool {
	left = canonicalizePermissionRefs(left)
	right = canonicalizePermissionRefs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || left[i].Member != right[i].Member {
			return false
		}
	}
	return true
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
