package semantic

import (
	"bytes"
	"fmt"
	"reflect"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/statecache"
)

//go:generate go run ./stategen

var semanticStateRegistry = sync.OnceValue(func() *statecache.Registry {
	registry := statecache.New(semanticStateTypes(), func(t reflect.Type, name string) bool {
		// The callback is reconstructed from the restored analyzer and options. No
		// executable code or process address is ever part of the disk state.
		return t != reflect.TypeFor[RetainedAnalysis]() || name != "post"
	}, func(v reflect.Value, i int) reflect.Value { return settable(v.Field(i)) })
	registerSemanticSliceAllocators(registry)
	registry.EnableParallelDecode()
	registry.InternZeroPointer(invalidType)
	registry.InternZeroPointer(neverType)
	registry.InternZeroPointer(nullType)
	return registry
})

// MarshalState snapshots an idle analysis. All fields must be representable;
// unsupported aliases or invocation resources reject publication entirely.
// The CLI admits it only in the opt-in disk-cache mode during qualification.
func (r *RetainedAnalysis) MarshalState() ([]byte, error) {
	if r == nil || r.a == nil || r.result == nil || r.rbUpdating || r.indexProofsUpdating || r.indexProofsByFunc != nil || r.indexProofsByDecl != nil || r.depth != 0 {
		return nil, fmt.Errorf("analysis is not idle")
	}
	if r.a.exprFactsOverlay != nil {
		return nil, fmt.Errorf("construction expression-facts overlay cannot be persisted/restored")
	}
	if r.a.exprTypeOverlay != nil {
		return nil, fmt.Errorf("construction expression-type overlay cannot be persisted")
	}
	if r.a.currentIndexProofContext || r.a.currentIndexProofHeaderOwner != nil || r.a.currentIndexProofOrigin != nil || r.a.currentIndexProofHeader {
		return nil, fmt.Errorf("analysis still owns an index-proof context")
	}
	if len(r.a.storageViewLoopUseFrames) != 0 || len(r.a.returnBorrowAliasFrames) != 0 || len(r.a.returnBorrowAliasJournal) != 0 || len(r.a.returnBorrowDeferFrames) != 0 || r.a.smtSolver != nil || r.result.shared != nil {
		return nil, fmt.Errorf("analysis still owns invocation resources")
	}
	if r.a.smtUnavailable {
		return nil, fmt.Errorf("solver availability was transient; retry analysis in the next process")
	}
	r.captureBodyUses()
	r.precomputeBaselineDigests()
	r.pre = r.pre.comparisonState()
	return r.marshalComparisonProjection()
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
	if r.a.exprFactsOverlay != nil {
		return nil, fmt.Errorf("persisted construction expression-facts overlay is unsupported")
	}
	if r.a.exprTypeOverlay != nil {
		return nil, fmt.Errorf("persisted construction expression-type overlay is unsupported")
	}
	r.pre = r.pre.comparisonState()
	r.post = func(decls []scopedDecl, appended map[*ast.FuncDecl]*FuncType) {
		r.a.runPostPhases(decls, r.options, nil, appended)
	}
	return r, nil
}

// MatchesInvocation guards the source root and semantic options in addition to
// the CLI's compiler/environment/toolchain cache identity.
func (r *RetainedAnalysis) MatchesInvocation(filename string, options AnalyzeOptions) bool {
	return r != nil && r.result != nil && r.result.File != nil && r.result.File.Filename == filename && reflect.DeepEqual(r.options, options)
}

func (r *RetainedAnalysis) ResultForSource(source []byte) (*Result, bool) {
	if r == nil || r.a == nil || r.result == nil || !bytes.Equal(r.src, source) {
		return nil, false
	}
	return r.result, true
}
