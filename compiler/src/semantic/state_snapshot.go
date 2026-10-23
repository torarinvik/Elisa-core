package semantic

import (
	"fmt"
	"reflect"
	"sync"

	"elisacore/src/statecache"
)

//go:generate go run ./stategen

var semanticStateRegistry = sync.OnceValue(func() *statecache.Registry {
	registry := statecache.New(semanticStateTypes(), func(t reflect.Type, name string) bool {
		// The callback is reconstructed from the restored analyzer and options. No
		// executable code or process address is ever part of the disk state.
		return t != reflect.TypeFor[RetainedAnalysis]() || name != "post"
	}, func(v reflect.Value, i int) reflect.Value { return settable(v.Field(i)) })
	registry.InternZeroPointer(invalidType)
	registry.InternZeroPointer(neverType)
	registry.InternZeroPointer(nullType)
	return registry
})

// MarshalState snapshots an idle analysis. All fields must be representable;
// unsupported aliases or invocation resources reject publication entirely.
// This is currently a qualification API, not automatic CLI cache admission.
func (r *RetainedAnalysis) MarshalState() ([]byte, error) {
	if r == nil || r.a == nil || r.result == nil || r.rbUpdating || r.depth != 0 {
		return nil, fmt.Errorf("analysis is not idle")
	}
	if len(r.a.returnBorrowAliasFrames) != 0 || len(r.a.returnBorrowAliasJournal) != 0 || len(r.a.returnBorrowDeferFrames) != 0 || r.a.smtSolver != nil || r.result.shared != nil {
		return nil, fmt.Errorf("analysis still owns invocation resources")
	}
	return semanticStateRegistry().Encode(r)
}

func RestoreAnalysisState(data []byte) (*RetainedAnalysis, error) {
	value, err := semanticStateRegistry().Decode(data)
	if err != nil {
		return nil, err
	}
	r, ok := value.(*RetainedAnalysis)
	if !ok || r == nil || r.a == nil || r.result == nil || r.result.analyzer != r.a || r.a.retained != r {
		return nil, fmt.Errorf("incomplete semantic state")
	}
	r.post = func(decls []scopedDecl) { r.a.runPostPhases(decls, r.options, nil) }
	return r, nil
}
