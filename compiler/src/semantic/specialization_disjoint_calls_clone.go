package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sync"
)

// The reflective clone independently copies both map levels but preserves each
// slice backing with its capacity limited to length. Match that ownership rule.
var specializationDisjointCallsSchema = sync.OnceValue(func() bool {
	field, ok := reflect.TypeFor[Analyzer]().FieldByName("disjointCallSitesByCaller")
	return ok && field.Type == reflect.TypeFor[map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation]()
})

func cloneSpecializationDisjointCallSites(src map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation) map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation {
	return cloneSpecializationDisjointCallSitesGuarded(src, specializationDisjointCallsSchema())
}

func cloneSpecializationDisjointCallSitesGuarded(src map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation, allowed bool) map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation)
	}
	if src == nil {
		return nil
	}
	dst := make(map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation, len(src))
	for caller, entries := range src {
		if entries == nil {
			dst[caller] = nil
			continue
		}
		copied := make(map[*ast.FuncDecl][]callDisjointObservation, len(entries))
		for callee, observations := range entries {
			if observations != nil {
				observations = observations[:len(observations):len(observations)]
			}
			copied[callee] = observations
		}
		dst[caller] = copied
	}
	return dst
}
