//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>
#include <llvm-c/DebugInfo.h>

// elisacoreTagAutovecLoop attaches `!llvm.loop` metadata to a loop's latch branch carrying an
// `elisa.autovec.expected` marker plus the source position. The marker rides in the IR, so it
// survives inlining (unlike a held function reference) and lets a post-optimization pass identify
// a comprehension build loop that was lowered to be vectorizable but failed to auto-vectorize.
static void elisacoreTagAutovecLoop(LLVMContextRef ctx, LLVMValueRef branchInst, const char *posText, const char *reasonText) {
	if (ctx == NULL || branchInst == NULL || posText == NULL) {
		return;
	}
	if (reasonText == NULL) {
		reasonText = "";
	}
	LLVMMetadataRef markerName = LLVMMDStringInContext2(ctx, "elisa.autovec.expected", 22);
	LLVMMetadataRef posMD = LLVMMDStringInContext2(ctx, posText, strlen(posText));
	LLVMMetadataRef reasonMD = LLVMMDStringInContext2(ctx, reasonText, strlen(reasonText));
	LLVMMetadataRef markerOps[3] = {markerName, posMD, reasonMD};
	LLVMMetadataRef markerNode = LLVMMDNodeInContext2(ctx, markerOps, 3);

	// Loop metadata must be a distinct, self-referential node: !{ self, <props...> }. Build it with
	// a temporary first operand, then RAUW the temporary to the node itself (which also deletes it).
	LLVMMetadataRef temp = LLVMTemporaryMDNode(ctx, NULL, 0);
	LLVMMetadataRef loopOps[2] = {temp, markerNode};
	LLVMMetadataRef loopID = LLVMMDNodeInContext2(ctx, loopOps, 2);
	LLVMMetadataReplaceAllUsesWith(temp, loopID);

	unsigned kind = LLVMGetMDKindIDInContext(ctx, "llvm.loop", 9);
	LLVMSetMetadata(branchInst, kind, LLVMMetadataAsValue(ctx, loopID));
}

static unsigned elisacoreLoopMDKind(LLVMContextRef ctx) {
	return LLVMGetMDKindIDInContext(ctx, "llvm.loop", 9);
}

// LLVM also attaches `llvm.loop.isvectorized` to scalar unrolled remainder loops. Treat that
// marker as proof of vectorization only when the loop latch's block actually contains a vector
// typed instruction; otherwise -Wperf would silently bless a scalar unroll as SIMD.
static int elisacoreBasicBlockHasVectorInstruction(LLVMBasicBlockRef block) {
	if (block == NULL) {
		return 0;
	}
	for (LLVMValueRef inst = LLVMGetFirstInstruction(block); inst != NULL; inst = LLVMGetNextInstruction(inst)) {
		LLVMTypeRef resultType = LLVMTypeOf(inst);
		if (resultType != NULL && LLVMGetTypeKind(resultType) == LLVMVectorTypeKind) {
			return 1;
		}
		unsigned operands = LLVMGetNumOperands(inst);
		for (unsigned i = 0; i < operands; i++) {
			LLVMValueRef operand = LLVMGetOperand(inst, i);
			LLVMTypeRef operandType = operand == NULL ? NULL : LLVMTypeOf(operand);
			if (operandType != NULL && LLVMGetTypeKind(operandType) == LLVMVectorTypeKind) {
				return 1;
			}
		}
	}
	return 0;
}

static int elisacoreFunctionHasVectorInstruction(LLVMValueRef function) {
	if (function == NULL) {
		return 0;
	}
	for (LLVMBasicBlockRef block = LLVMGetFirstBasicBlock(function); block != NULL; block = LLVMGetNextBasicBlock(block)) {
		if (elisacoreBasicBlockHasVectorInstruction(block)) {
			return 1;
		}
	}
	return 0;
}
*/
import "C"

import (
	"fmt"
	"reflect"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// comprehensionAliasContextForLoop recognizes the backend's fresh-result map
// lowering. The synthesized loop contains a source element binding and a
// destination indexed store, both over plain identifiers. Those two darrays are
// distinct because the destination was freshly allocated before the loop. Do not
// apply this fact to self-extend or user-written loops: a false no-alias claim is a
// silent miscompile, so failure to recognize the exact shape stays conservative.
func (s *functionState) comprehensionAliasContextForLoop(stmt *ast.ForStmt) *comprehensionAliasContext {
	if s == nil || stmt == nil || stmt.AutovecReason != "comprehension map" {
		return nil
	}
	var sourceName, destinationName string
	for _, bodyStmt := range stmt.Body {
		switch n := bodyStmt.(type) {
		case *ast.VarDeclStmt:
			if sourceName == "" {
				if index, ok := n.Value.(*ast.IndexExpr); ok {
					if object, ok := index.Object.(*ast.Ident); ok {
						sourceName = object.Name
					}
				}
			}
		case *ast.AssignStmt:
			if destinationName == "" {
				if index, ok := n.Target.(*ast.IndexExpr); ok {
					if object, ok := index.Object.(*ast.Ident); ok {
						destinationName = object.Name
					}
				}
			}
		}
	}
	if sourceName == "" || destinationName == "" || sourceName == destinationName {
		return nil
	}
	sequence := 0
	if s.g != nil {
		sequence = s.g.syntheticCounter
		s.g.syntheticCounter++
	}
	return &comprehensionAliasContext{
		domainName:       fmt.Sprintf("elisa.comprehension.%d", sequence),
		sourceName:       sourceName,
		destinationName:  destinationName,
		sourceScope:      "source",
		destinationScope: "destination",
	}
}

// permissionRefsGrantScalar reports whether a permission list grants the Scalar family — either
// the bare-family spelling `can Scalar` or any member spelling (`can Scalar.Loop`). Used to
// suppress expected-to-vectorize loop tagging inside the grant's lexical extent.
func permissionRefsGrantScalar(refs []ast.PermissionRef) bool {
	for _, ref := range refs {
		if ref.Name == "Scalar" {
			return true
		}
	}
	return false
}

// userLoopVectorEligible reports whether a USER-WRITTEN loop body is the clean element-wise shape
// the vectorizer should handle: straight-line (no branches, nested loops, breaks, or returns),
// call-free, and containing at least one indexed store (`dst[i] <- value`). Loops outside this
// shape are NOT tagged — a call, branch, or I/O in the body already explains scalar execution, so
// demanding `can Scalar` there would be noise, and accumulator reductions (`s <- s + x`, an
// assignment to a bare name) are excluded because a strict-FP reduction legitimately cannot
// vectorize without reassociation (the fold-comprehension form is the vectorizable spelling).
func userLoopVectorEligible(body []ast.Stmt) bool {
	hasIndexedStore := false
	indexedObjects := map[string]bool{}
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if n.Value != nil && ast.ExprContainsCall(n.Value) {
				return false
			}
			if !collectUserLoopIndexedObjects(n.Value, indexedObjects) {
				return false
			}
		case *ast.ExprStmt:
			if ast.ExprContainsCall(n.Expr) {
				return false
			}
			if !collectUserLoopIndexedObjects(n.Expr, indexedObjects) {
				return false
			}
		case *ast.AssignStmt:
			if n.Optional || ast.ExprContainsCall(n.Target) || ast.ExprContainsCall(n.Value) {
				return false
			}
			if !collectUserLoopIndexedObjects(n.Target, indexedObjects) || !collectUserLoopIndexedObjects(n.Value, indexedObjects) {
				return false
			}
			if _, ok := n.Target.(*ast.IndexExpr); ok {
				hasIndexedStore = true
			} else {
				// Assignment to a non-indexed target (bare accumulator, field) — a loop-carried
				// scalar dependency the vectorizer can't (or shouldn't be demanded to) handle.
				return false
			}
		default:
			return false
		}
	}
	// Without a proof that two buffers are disjoint, LLVM must conservatively keep an alias
	// check (and may legitimately leave the loop scalar). Marking such a loop as an obligation
	// would turn a valid program into a false -Wperf failure. Single-buffer loops remain sound:
	// the loop-carried accesses are to one known storage object and LLVM can vectorize or reject
	// them based on the operation itself. A future proof-backed disjointness fact can widen this
	// gate without changing the diagnostic contract.
	return hasIndexedStore && len(indexedObjects) <= 1
}

// collectUserLoopIndexedObjects records the named containers used by indexed accesses in one
// straight-line user-loop statement. An unknown/indexed receiver is rejected conservatively: it
// may hide a second buffer or an aliasing field projection that the local shape check cannot prove.
func collectUserLoopIndexedObjects(value ast.Expr, objects map[string]bool) bool {
	if value == nil {
		return true
	}
	seen := map[uintptr]bool{}
	var walk func(reflect.Value) bool
	walk = func(current reflect.Value) bool {
		if !current.IsValid() {
			return true
		}
		switch current.Kind() {
		case reflect.Interface:
			if current.IsNil() {
				return true
			}
			return walk(current.Elem())
		case reflect.Pointer:
			if current.IsNil() {
				return true
			}
			if ptr := current.Pointer(); ptr != 0 {
				if seen[ptr] {
					return true
				}
				seen[ptr] = true
			}
			if index, ok := current.Interface().(*ast.IndexExpr); ok {
				ident, ok := index.Object.(*ast.Ident)
				if !ok || ident == nil || ident.Name == "" {
					return false
				}
				objects[ident.Name] = true
				return walk(reflect.ValueOf(index.Index))
			}
			return walk(current.Elem())
		case reflect.Struct:
			for i := 0; i < current.NumField(); i++ {
				if !walk(current.Field(i)) {
					return false
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < current.Len(); i++ {
				if !walk(current.Index(i)) {
					return false
				}
			}
		}
		return true
	}
	return walk(reflect.ValueOf(value))
}

// tagAutovecExpectedLoop marks a loop's latch branch as expected-to-vectorize (compiler-synthesized
// comprehension builds via ForStmt.AutovecExpected, plus vector-eligible user loops). No-op at -O0,
// where no vectorization runs, and inside a `can Scalar` grant, which is the sanctioned way to
// accept a scalar loop (warning otherwise; hard error under -Wperf).
func (s *functionState) tagAutovecExpectedLoop(branchInst C.LLVMValueRef, pos lexer.Pos, reason string) {
	if s == nil || s.g == nil || s.g.optLevel == OptimizationLevel0 || branchInst == nil {
		return
	}
	if s.scalarGrantDepth > 0 {
		return
	}
	if reason == "" {
		reason = "comprehension"
	}
	posC := cString(pos.String())
	defer C.free(unsafe.Pointer(posC))
	reasonC := cString(reason)
	defer C.free(unsafe.Pointer(reasonC))
	C.elisacoreTagAutovecLoop(s.g.context, branchInst, posC, reasonC)
}

// verifyAutovecExpectations runs after the optimization pipeline. It scans every loop's `!llvm.loop`
// metadata for the `elisa.autovec.expected` marker and warns for any marked loop that lacks a real
// vector instruction in its latch block. LLVM stamps `isvectorized` on scalar-unrolled loops too, so
// that flag alone is not proof that SIMD was generated. The marker rides in the IR and is found
// wherever inlining moved the loop; warnings are deduplicated by source position.
func (g *llvmGenerator) verifyAutovecExpectations() {
	if g == nil || g.module == nil || g.optLevel == OptimizationLevel0 {
		return
	}
	loopKind := C.elisacoreLoopMDKind(g.context)
	seen := map[string]bool{}
	for fn := C.LLVMGetFirstFunction(g.module); fn != nil; fn = C.LLVMGetNextFunction(fn) {
		for bb := C.LLVMGetFirstBasicBlock(fn); bb != nil; bb = C.LLVMGetNextBasicBlock(bb) {
			term := C.LLVMGetBasicBlockTerminator(bb)
			if term == nil {
				continue
			}
			loopMD := C.LLVMGetMetadata(term, loopKind)
			if loopMD == nil {
				continue
			}
			pos, reason, marked, vectorized := inspectAutovecLoopMetadata(loopMD)
			if vectorized && C.elisacoreBasicBlockHasVectorInstruction(bb) == 0 && C.elisacoreFunctionHasVectorInstruction(fn) == 0 {
				vectorized = false
			}
			if !marked || vectorized || seen[pos] {
				continue
			}
			seen[pos] = true
			enforce := g.result != nil && g.result.EnforcePerfLints
			g.perfWarnings = append(g.perfWarnings, autovecPerfWarning(pos, reason, int(g.optLevel), enforce))
		}
	}
}

// inspectAutovecLoopMetadata walks a loop-id MDNode's property operands, returning the marker's
// embedded source position (if present), whether the marker is present, and whether the loop also
// carries `llvm.loop.isvectorized`.
func inspectAutovecLoopMetadata(loopMD C.LLVMValueRef) (pos string, reason string, marked bool, vectorized bool) {
	ops := mdNodeOperands(loopMD)
	// Operand 0 is the self-reference; properties follow.
	for i := 1; i < len(ops); i++ {
		prop := ops[i]
		if C.LLVMIsAMDNode(prop) == nil {
			continue
		}
		propOps := mdNodeOperands(prop)
		if len(propOps) == 0 {
			continue
		}
		name, ok := mdStringValue(propOps[0])
		if !ok {
			continue
		}
		switch name {
		case "elisa.autovec.expected":
			marked = true
			if len(propOps) >= 2 {
				if p, ok := mdStringValue(propOps[1]); ok {
					pos = p
				}
			}
			if len(propOps) >= 3 {
				if r, ok := mdStringValue(propOps[2]); ok {
					reason = r
				}
			}
		case "llvm.loop.isvectorized":
			vectorized = true
		}
	}
	return pos, reason, marked, vectorized
}

// autovecPerfWarning renders the -Wperf message for a marked-but-unvectorized loop, naming the
// construct (from the embedded reason) and the most likely blocker for that construct. The loop was
// only marked because its body is call-free and its shape was lowered to be vectorizer-legal, so a
// real failure is almost always a memory dependency the vectorizer could not disprove.
func autovecPerfWarning(pos, reason string, optLevel int, enforce bool) string {
	construct := reason
	if construct == "" {
		construct = "comprehension"
	}
	hint := "check for an aliasing or loop-carried dependency, or a body the vectorizer cost model rejected"
	switch reason {
	case "fold reduction":
		hint = "the reduction did not re-bracket into vector lanes — check for an aliasing source or a " +
			"loop-carried dependency other than the accumulator"
	case "comprehension map":
		hint = "the element store did not vectorize — check whether the source and destination may alias, " +
			"or for a loop-carried dependency in the element transform"
	case "loop":
		hint = "the element store did not vectorize — check for aliasing between the arrays, a loop-carried " +
			"dependency, or a shape the vectorizer cost model rejected"
	}
	severity := "warning"
	if enforce {
		severity = "error"
	}
	return fmt.Sprintf("%s: %s [-Wperf]: %s was lowered for auto-vectorization but did not vectorize "+
		"at -O%d; %s. If scalar execution is intended, wrap it in a `can Scalar:` block (or grant "+
		"`can[Scalar]` on the function)", pos, severity, construct, optLevel, hint)
}

// mdNodeOperands returns the operands of an MDNode-as-value, or nil if it is not a node.
func mdNodeOperands(v C.LLVMValueRef) []C.LLVMValueRef {
	if v == nil || C.LLVMIsAMDNode(v) == nil {
		return nil
	}
	n := int(C.LLVMGetMDNodeNumOperands(v))
	if n == 0 {
		return nil
	}
	ops := make([]C.LLVMValueRef, n)
	C.LLVMGetMDNodeOperands(v, &ops[0])
	return ops
}

// mdStringValue reads an MDString-as-value, returning ok=false if it is not a string.
func mdStringValue(v C.LLVMValueRef) (string, bool) {
	if v == nil {
		return "", false
	}
	var length C.unsigned
	cs := C.LLVMGetMDString(v, &length)
	if cs == nil {
		return "", false
	}
	return C.GoStringN(cs, C.int(length)), true
}

// PerfWarnings returns the performance-friction warnings collected during optimized code generation
// (currently the auto-vectorization verifier). Empty unless the build optimized.
func (g *llvmGenerator) PerfWarnings() []string {
	if g == nil {
		return nil
	}
	return g.perfWarnings
}
