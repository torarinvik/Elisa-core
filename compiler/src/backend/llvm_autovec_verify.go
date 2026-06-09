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
static void elisacoreTagAutovecLoop(LLVMContextRef ctx, LLVMValueRef branchInst, const char *posText) {
	if (ctx == NULL || branchInst == NULL || posText == NULL) {
		return;
	}
	LLVMMetadataRef markerName = LLVMMDStringInContext2(ctx, "elisa.autovec.expected", 22);
	LLVMMetadataRef posMD = LLVMMDStringInContext2(ctx, posText, strlen(posText));
	LLVMMetadataRef markerOps[2] = {markerName, posMD};
	LLVMMetadataRef markerNode = LLVMMDNodeInContext2(ctx, markerOps, 2);

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

	"elisacore/src/lexer"
)

// tagAutovecExpectedLoop marks a compiler-synthesized comprehension build loop's latch branch as
// expected-to-vectorize (see ForStmt.AutovecExpected). No-op at -O0, where no vectorization runs.
func (s *functionState) tagAutovecExpectedLoop(branchInst C.LLVMValueRef, pos lexer.Pos) {
	if s == nil || s.g == nil || s.g.optLevel == OptimizationLevel0 || branchInst == nil {
		return
	}
	posC := cString(pos.String())
	defer C.free(unsafe.Pointer(posC))
	C.elisacoreTagAutovecLoop(s.g.context, branchInst, posC)
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
			pos, marked, vectorized := inspectAutovecLoopMetadata(loopMD)
			if !marked || vectorized || seen[pos] {
				continue
			}
			seen[pos] = true
			g.perfWarnings = append(g.perfWarnings, fmt.Sprintf(
				"%s: warning [-Wperf]: comprehension was lowered for auto-vectorization but did not "+
					"vectorize at -O%d; check for an aliasing or loop-carried dependency, or a body the "+
					"vectorizer cost model rejected", pos, int(g.optLevel)))
		}
	}
}

// inspectAutovecLoopMetadata walks a loop-id MDNode's property operands, returning the marker's
// embedded source position (if present), whether the marker is present, and whether the loop also
// carries `llvm.loop.isvectorized`.
func inspectAutovecLoopMetadata(loopMD C.LLVMValueRef) (pos string, marked bool, vectorized bool) {
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
		case "llvm.loop.isvectorized":
			vectorized = true
		}
	}
	return pos, marked, vectorized
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
