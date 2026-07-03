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
*/
import "C"

import (
	"fmt"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if n.Value != nil && ast.ExprContainsCall(n.Value) {
				return false
			}
		case *ast.ExprStmt:
			if ast.ExprContainsCall(n.Expr) {
				return false
			}
		case *ast.AssignStmt:
			if n.Optional || ast.ExprContainsCall(n.Target) || ast.ExprContainsCall(n.Value) {
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
	return hasIndexedStore
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
// metadata for the `elisa.autovec.expected` marker and warns for any marked loop that lacks
// `llvm.loop.isvectorized`. LLVM stamps `isvectorized` on the vector body AND the scalar remainder
// of every loop it vectorizes, so a marked loop without it genuinely fell back to scalar code — a
// real efficiency regression. Because the marker rides in the IR it is found wherever inlining moved
// the loop, and because the check keys on isvectorized there are no false positives on a vectorized
// loop's remainder. Warnings are deduplicated by source position.
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
