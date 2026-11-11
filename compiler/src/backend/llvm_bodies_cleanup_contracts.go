//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) emitScopedCleanup(binding scopedCleanupBinding) error {
	if binding.kind == scopedCleanupDeferBody {
		return s.emitDeferredBody(binding.deferBody)
	}
	if binding.kind == scopedCleanupRegion {
		// The matching region binding is marked scope-owned, so this is the sole free on the
		// function's normal and abrupt scope exits. Function-owned regions are not registered here.
		return s.emitArenaFree(binding.ptr, binding.typ)
	}
	if binding.kind == scopedCleanupRegionReset {
		// Keep the blocks for next iteration; the function-return arena_free releases them.
		return s.emitArenaReset(binding.ptr, binding.typ)
	}
	if binding.kind == scopedCleanupDrop {
		// docs/126 D1: run the user's `__drop__` unless the value was moved away.
		return s.emitConditionalDrop(binding)
	}
	if binding.kind == scopedCleanupTaskGroupWait {
		wait, waitType, err := s.ensureRuntimeFunction("task_group_wait_all", nil)
		if err != nil {
			return err
		}
		waitLLVMType, err := s.g.lowerFunctionType(waitType)
		if err != nil {
			return err
		}
		s.buildCall(waitLLVMType, wait, []llvmIRValueRef{binding.ptr}, "")
		return nil
	}
	ops := semantic.CreateTypeBoundOps(binding.typ)
	if len(ops) == 0 {
		switch binding.kind {
		case scopedCleanupLockGuard:
			return s.emitConditionalMutexUnlock(binding)
		case scopedCleanupThreadPool:
			return s.emitConditionalPoolShutdown(binding)
		default:
			return fmt.Errorf("unsupported scoped cleanup kind %d", binding.kind)
		}
	}
	for _, op := range ops {
		if len(op.Path) != 0 || op.IsFillSeq() {
			return fmt.Errorf("unsupported synthesized scoped cleanup path for %q", binding.name)
		}
		switch op.Kind {
		case semantic.TypeBoundCleanupMutexUnlock:
			if err := s.emitConditionalMutexUnlock(binding); err != nil {
				return err
			}
		case semantic.TypeBoundCleanupThreadPoolShutdown:
			if err := s.emitConditionalPoolShutdown(binding); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported synthesized scoped cleanup kind %q", op.Kind)
		}
	}
	return nil
}
func defineBindingInCodegenScope(scope *codegenScope, name string, binding valueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.bindingName == "" || scope.bindingName == name {
		scope.bindingName = name
		scope.binding = binding
		return
	}
	if scope.bindings == nil {
		scope.bindings = map[string]valueBinding{}
	}
	scope.bindings[name] = binding
}
func bindPackedCommonFieldValueInCodegenScope(scope *codegenScope, name string, binding packedCommonFieldValueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedCommonValueName == "" || scope.packedCommonValueName == name {
		scope.packedCommonValueName = name
		scope.packedCommonValueBinding = binding
		return
	}
	if scope.packedCommonValues == nil {
		scope.packedCommonValues = map[string]packedCommonFieldValueBinding{}
	}
	scope.packedCommonValues[name] = binding
}
func bindPackedEnumStorageInCodegenScope(scope *codegenScope, name string, binding packedEnumStorageBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumPtrs == nil {
		scope.packedEnumPtrs = map[string]packedEnumStorageBinding{}
	}
	scope.packedEnumPtrs[name] = binding
}
func bindPackedEnumStoreInCodegenScope(scope *codegenScope, name string, binding packedStoreBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumStoreName == "" || scope.packedEnumStoreName == name {
		scope.packedEnumStoreName = name
		scope.packedEnumStoreBinding = binding
		return
	}
	if scope.packedEnumStores == nil {
		scope.packedEnumStores = map[string]packedStoreBinding{}
	}
	scope.packedEnumStores[name] = binding
}
func bindPackedViewInCodegenScope(scope *codegenScope, name string, binding packedVariantViewBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedViewName == "" || scope.packedViewName == name {
		scope.packedViewName = name
		scope.packedViewBinding = binding
		return
	}
	if scope.packedViewPtrs == nil {
		scope.packedViewPtrs = map[string]packedVariantViewBinding{}
	}
	scope.packedViewPtrs[name] = binding
}
func cloneValueBindingMap(src map[string]valueBinding) map[string]valueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]valueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedCommonBindingMap(src map[string]packedCommonFieldValueBinding) map[string]packedCommonFieldValueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedCommonFieldValueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedEnumStorageBindingMap(src map[string]packedEnumStorageBinding) map[string]packedEnumStorageBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedEnumStorageBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedStoreBindingMap(src map[string]packedStoreBinding) map[string]packedStoreBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedStoreBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedViewBindingMap(src map[string]packedVariantViewBinding) map[string]packedVariantViewBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedVariantViewBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}

// emitOldCaptures evaluates every `old(expr)` appearing in the function's `ensure` clauses at entry
// and records the value, so postcondition checks at each return read the entry-time value. Debug
// builds only. The captured SSA values are computed in the entry region and dominate all returns.
func (s *functionState) emitOldCaptures(decl *ast.FuncDecl) error {
	if decl == nil || len(decl.EnsureValues) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	var olds []*ast.CallExpr
	for _, e := range decl.EnsureValues {
		if s.g.result != nil && s.g.semanticReadView().ValueGhostContracts(e) {
			continue
		}
		collectOldCalls(e, &olds)
	}
	if len(olds) == 0 {
		return nil
	}
	s.oldCaptures = make(map[*ast.CallExpr]oldCapture, len(olds))
	for _, oc := range olds {
		if len(oc.Args) != 1 {
			continue
		}
		// Capture the entry-time *value*, not a reference. `old(p)` where p is a `T&` param must
		// snapshot the pointee NOW (at entry); storing the pointer would re-read the same address at
		// the return check and always equal the current value — and it would also type-mismatch the
		// auto-deref'd operand. Coerce to the pointee type so emitExpr loads through the ref here.
		expected := s.exprType(oc.Args[0])
		if rt, ok := expected.(*semantic.RefType); ok && rt != nil {
			expected = rt.Elem
		}
		val, typ, err := s.emitExpr(oc.Args[0], expected)
		if err != nil {
			return err
		}
		s.oldCaptures[oc] = oldCapture{val: val, typ: typ}
	}
	return nil
}

// collectOldCalls appends every `old(...)` pseudo-call reachable in expr to out (depth-first over the
// common expression shapes used in contracts).
func collectOldCalls(expr ast.Expr, out *[]*ast.CallExpr) {
	switch n := expr.(type) {
	case nil:
		return
	case *ast.CallExpr:
		if ast.IsOldCall(n) {
			*out = append(*out, n)
			return // don't descend into the captured expression
		}
		collectOldCalls(n.Func, out)
		for _, a := range n.Args {
			collectOldCalls(a, out)
		}
	case *ast.BinaryExpr:
		collectOldCalls(n.Left, out)
		collectOldCalls(n.Right, out)
	case *ast.UnaryExpr:
		collectOldCalls(n.Operand, out)
	case *ast.ParenExpr:
		collectOldCalls(n.Inner, out)
	case *ast.FieldExpr:
		collectOldCalls(n.Object, out)
	case *ast.IndexExpr:
		collectOldCalls(n.Object, out)
		collectOldCalls(n.Index, out)
	}
}

// emitPreconditionChecks emits a function's `requires` value-contracts at entry, in debug builds
// only (-O0, or forced via ELISACORE_FORCE_CONTRACTS). Zero cost in release: "debug verifies what
// release assumes." Params are already bound, so the conditions resolve normally.
func (s *functionState) emitPreconditionChecks(decl *ast.FuncDecl) error {
	if decl == nil || len(decl.Requires) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	for i, cond := range decl.Requires {
		if cond == nil || (s.g.result != nil && s.g.semanticReadView().ValueGhostContracts(cond)) {
			continue
		}
		if proofAt(decl.RequiresProofs, i) != nil {
			continue
		}
		if err := s.emitContractCheck(cond, "precondition failed"); err != nil {
			return err
		}
	}
	return nil
}

// emitPostconditionChecks emits a function's `ensure` value-contracts just before a return, in
// debug builds only. `result` is bound to the returned value so the conditions can reference it,
// and `old(...)` reads the entry-time snapshot captured by emitOldCaptures.
func (s *functionState) emitPostconditionChecks(value llvmIRValueRef, actual semantic.Type) error {
	if s.decl == nil || len(s.decl.EnsureValues) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	// Bind `result` to the returned value (skip for void / error-union returns) so the ensure
	// conditions resolve it. The binding lives only for these checks; the block terminates on return.
	if s.fnType != nil && !isVoidType(s.fnType.Return) {
		if _, isUnion := s.fnType.Return.(*semantic.ErrorUnionType); !isUnion {
			alloca, err := s.createEntryAlloca("result", s.fnType.Return)
			if err != nil {
				return err
			}
			llvmBuildStore(s.builder, value, alloca)
			s.defineBinding("result", valueBinding{ptr: alloca, typ: s.fnType.Return, mutable: false})
		}
	}
	for i, cond := range s.decl.EnsureValues {
		if cond == nil || (s.g.result != nil && s.g.semanticReadView().ValueGhostContracts(cond)) {
			continue
		}
		if proofAt(s.decl.EnsureProofs, i) != nil {
			continue
		}
		if err := s.emitContractCheck(cond, "postcondition failed"); err != nil {
			return err
		}
	}
	return nil
}

func proofAt(proofs []*ast.ProofBlockStmt, i int) *ast.ProofBlockStmt {
	if i < 0 || i >= len(proofs) {
		return nil
	}
	return proofs[i]
}

// emitRefinementPostconditionChecks emits the runtime half of `ensures <param> is Law` (docs/85
// brick 2 B): for each refinement postcondition, a debug-gated check `Law(param)` that panics if
// false. Called at every exit — explicit returns AND implicit fall-through — so a void function
// that omits `return` is still checked, keeping the caller's gained predicate fact sound. Elided in
// release ("debug verifies what release assumes"). The static prove/refute half runs in the
// analyzer (dischargeEnsuresRefinements); a statically-proven case still gets this (harmless,
// debug-only) check.
func (s *functionState) emitRefinementPostconditionChecks() error {
	if s.decl == nil || len(s.decl.Ensures) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	for _, clause := range s.decl.Ensures {
		if clause.Kind != ast.EnsuresKindRefinement || clause.RefinementLaw == "" || clause.Target.Root == "" {
			continue
		}
		// Subject first, then the parametric postcondition's constant args (`is Bounded[0, 500]`).
		args := []ast.Expr{&ast.Ident{Position: clause.Position, Name: clause.Target.Root}}
		args = append(args, clause.RefinementArgs...)
		call := &ast.CallExpr{
			Position: clause.Position,
			Func:     &ast.Ident{Position: clause.Position, Name: clause.RefinementLaw},
			Args:     args,
		}
		if err := s.emitContractCheck(call, "postcondition failed"); err != nil {
			return err
		}
	}
	return nil
}

// emitContractCheck lowers a single boolean contract: if the condition is false, panic with a
// backtrace at the contract's source location; otherwise fall through.
func (s *functionState) emitContractCheck(cond ast.Expr, label string) error {
	condVal, _, err := s.emitExpr(cond, nil)
	if err != nil {
		return err
	}
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("contract.ok"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("contract.fail"))
	llvmBuildCondBr(s.builder, condVal, okBB, failBB)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitPanicWithBacktrace(cond.Pos(), &ast.StringLit{Position: cond.Pos(), Value: label}); err != nil {
		return err
	}
	llvmPositionBuilderAtEnd(s.builder, okBB)
	return nil
}

// blockReachableFromEntry walks the CFG forward from fn's entry block. A conditional branch on a
// constant follows only the edge it takes, so the exit of a `while true` that leaves only by
// `return` is unreachable.
func blockReachableFromEntry(fn llvmIRValueRef, target llvmIRBlockRef) bool {
	entry := llvmGetEntryBasicBlock(fn)
	seen := map[llvmIRBlockRef]bool{entry: true}
	work := []llvmIRBlockRef{entry}
	for len(work) != 0 {
		block := work[len(work)-1]
		work = work[:len(work)-1]
		if block == target {
			return true
		}
		term := llvmGetBasicBlockTerminator(block)
		if term == nil {
			continue
		}
		successors := int(llvmGetNumSuccessors(term))
		only := -1
		if llvmIsABranchInst(term) != nil && llvmIsConditional(term) != 0 {
			if cond := llvmGetCondition(term); cond != nil && llvmIsAConstantInt(cond) != nil {
				// Successor 0 is the true destination, successor 1 the false one.
				only = 1
				if llvmConstIntGetZExtValue(cond) != 0 {
					only = 0
				}
			}
		}
		for i := 0; i < successors; i++ {
			if only >= 0 && i != only {
				continue
			}
			next := llvmGetSuccessor(term, llvmIRUnsigned(i))
			if next != nil && !seen[next] {
				seen[next] = true
				work = append(work, next)
			}
		}
	}
	return false
}

// suffixIndex maps the text after the last '.' of every key with a non-nil value
// to those keys. A dot-free name has a unique `"." + name` suffix match exactly
// when its entry holds one key.
func suffixIndex[V comparable](entries map[string]V) map[string][]string {
	var zero V
	index := map[string][]string{}
	for key, value := range entries {
		if value == zero {
			continue
		}
		if dot := strings.LastIndex(key, "."); dot >= 0 {
			last := key[dot+1:]
			index[last] = append(index[last], key)
		}
	}
	return index
}

func (g *llvmGenerator) namedTypeSuffixIndex() map[string][]string {
	namedTypes := g.namedTypesMap("named-type-suffix-index")
	count := len(namedTypes)
	if shared := g.sharedLookups; shared != nil && count == shared.namedCount {
		shared = shared.programLookups()
		shared.namedOnce.Do(func() { shared.namedSuffixes = suffixIndex(namedTypes) })
		return shared.namedSuffixes
	}
	if g.namedTypeSuffixes == nil || g.namedTypeSuffixesSize != count {
		g.namedTypeSuffixes = suffixIndex(namedTypes)
		g.namedTypeSuffixesSize = count
	}
	return g.namedTypeSuffixes
}

func (g *llvmGenerator) globalSuffixIndex() map[string][]string {
	symbols := g.globalSymbolsMap("global-symbol-suffix-index")
	if shared := g.sharedLookups; shared != nil && len(symbols) == shared.symbolCount {
		shared = shared.programLookups()
		shared.globalOnce.Do(func() { shared.globalSuffixes = suffixIndex(symbols) })
		return shared.globalSuffixes
	}
	if g.globalSuffixes == nil || g.globalSuffixesSize != len(symbols) {
		g.globalSuffixes = suffixIndex(symbols)
		g.globalSuffixesSize = len(symbols)
	}
	return g.globalSuffixes
}
