package semantic

import (
	"bytes"
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// Authorization keys deliberately remain family/member based. Semantic rows
// additionally retain the syntax of abstract type arguments and realizations.
// This identity is position independent but still requires resolved environment
// dependencies before it can authorize reuse across analyzer invocations.
type permissionInstantiationID struct {
	Digest  [32]byte
	Present bool
}

func permissionInstantiation(ref ast.PermissionRef) (permissionInstantiationID, bool) {
	if len(ref.TypeArgs) == 0 && len(ref.Via) == 0 {
		return permissionInstantiationID{}, true
	}
	value := struct {
		TypeArgs []ast.TypeExpr
		Via      []ast.PermissionRef
	}{ref.TypeArgs, ref.Via}
	digest, _, err := functionSyntaxFingerprint(value)
	return permissionInstantiationID{digest, true}, err == nil
}

type instantiatedPermissionKey struct {
	Name     string
	Instance permissionInstantiationID
}
type instantiatedMemberKey struct {
	Family instantiatedPermissionKey
	Member string
}

func canonicalizeInstantiatedPermissionRefs(refs []ast.PermissionRef) []ast.PermissionRef {
	families := map[instantiatedPermissionKey]ast.PermissionRef{}
	members := map[instantiatedMemberKey]ast.PermissionRef{}
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		id, ok := permissionInstantiation(ref)
		family := instantiatedPermissionKey{Name: ref.Name, Instance: id}
		if !ok {
			return canonicalizeUnencodablePermissionRefs(refs)
		}
		if ref.Member == "" {
			families[family] = clonePermissionRef(ref)
			for key := range members {
				if key.Family == family {
					delete(members, key)
				}
			}
		} else if _, covered := families[family]; !covered {
			members[instantiatedMemberKey{family, ref.Member}] = clonePermissionRef(ref)
		}
	}
	keys := make([]instantiatedMemberKey, 0, len(families)+len(members))
	for key := range families {
		keys = append(keys, instantiatedMemberKey{Family: key})
	}
	for key := range members {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Family.Name != b.Family.Name {
			return a.Family.Name < b.Family.Name
		}
		if a.Member != b.Member {
			return a.Member < b.Member
		}
		if a.Family.Instance.Present != b.Family.Instance.Present {
			return !a.Family.Instance.Present
		}
		if c := bytes.Compare(a.Family.Instance.Digest[:], b.Family.Instance.Digest[:]); c != 0 {
			return c < 0
		}
		return false
	})
	out := make([]ast.PermissionRef, 0, len(keys))
	for _, key := range keys {
		if key.Member == "" {
			out = append(out, families[key.Family])
		} else {
			out = append(out, members[key])
		}
	}
	return out
}

// Oversized syntax still has exact invocation-local equality. Falling back to
// structural comparison bounds set union instead of duplicating opaque
// effects at each recursive inference round. It supplies no durable digest.
func samePermissionInstantiationValue(a, b ast.PermissionRef) bool {
	x, xok := permissionInstantiation(a)
	y, yok := permissionInstantiation(b)
	if xok && yok {
		return x == y
	}
	return reflect.DeepEqual(a.TypeArgs, b.TypeArgs) && reflect.DeepEqual(a.Via, b.Via)
}

func canonicalizeUnencodablePermissionRefs(refs []ast.PermissionRef) []ast.PermissionRef {
	var out []ast.PermissionRef
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		covered := false
		for i := 0; i < len(out); {
			existing := out[i]
			if existing.Name != ref.Name || !samePermissionInstantiationValue(existing, ref) {
				i++
				continue
			}
			if existing.Member == "" || existing.Member == ref.Member {
				if existing.Member == ref.Member {
					out[i] = clonePermissionRef(ref)
				}
				covered = true
				break
			}
			if ref.Member == "" {
				out = append(out[:i], out[i+1:]...)
				continue
			}
			i++
		}
		if !covered {
			out = append(out, clonePermissionRef(ref))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Member < out[j].Member
	})
	return out
}

func sameUnorderedPermissionRows(left, right []ast.PermissionRef) bool {
	matched := make([]bool, len(right))
	for _, a := range left {
		found := false
		for i, b := range right {
			if !matched[i] && a.Name == b.Name && a.Member == b.Member && samePermissionInstantiationValue(a, b) {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
