//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import "elisacore/src/ast"

// emitRefinementChecks discharges a refinement-typed var declaration (docs/85 Stage 1c-2). For each
// recorded predicate call `P(x)` it emits a boundary check that traps when the predicate is false.
// This is the runtime tier of the discharge ladder — "debug verifies what release assumes": active
// in debug (-O0) or under -fbounds-check, a no-op in release. The static prover (1d) will discharge
// the provable predicates so only the genuinely-unprovable ones keep this check.
func (s *functionState) emitRefinementChecks(n *ast.VarDeclStmt) error {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.RefinementChecks == nil {
		return nil
	}
	checks := s.g.result.RefinementChecks[n]
	if len(checks) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceBoundsCheck {
		return nil
	}
	boolType := s.g.result.NamedTypes["bool"]
	for _, call := range checks {
		cond, _, err := s.emitExpr(call, boolType)
		if err != nil {
			return err
		}
		okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("refine.ok"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("refine.fail"))
		C.LLVMBuildCondBr(s.builder, cond, okBB, failBB)
		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if err := s.emitTrapUnreachable("refine.trap"); err != nil {
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	}
	return nil
}

// emitCallArgRefinementChecks discharges the runtime tier for unproven, side-effect-free arguments
// passed to refinement-typed parameters (docs/85: the function-contract boundary). Mirrors
// emitRefinementChecks but at the call site, emitted before the real call. Debug-only: active at -O0
// or under -fbounds-check, a no-op in release. The static prover already removed the provable args,
// so this traps only on a genuinely-unprovable argument that turned out to violate the contract.
func (s *functionState) emitCallArgRefinementChecks(expr *ast.CallExpr) error {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.CallArgRefinementChecks == nil {
		return nil
	}
	checks := s.g.result.CallArgRefinementChecks[expr]
	if len(checks) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceBoundsCheck {
		return nil
	}
	boolType := s.g.result.NamedTypes["bool"]
	for _, call := range checks {
		cond, _, err := s.emitExpr(call, boolType)
		if err != nil {
			return err
		}
		okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("argrefine.ok"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("argrefine.fail"))
		C.LLVMBuildCondBr(s.builder, cond, okBB, failBB)
		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if err := s.emitTrapUnreachable("argrefine.trap"); err != nil {
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	}
	return nil
}
