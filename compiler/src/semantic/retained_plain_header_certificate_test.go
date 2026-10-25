//go:build cgo

package semantic

import (
	"strconv"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestPlainHeaderCertificateRebindsExactTypedProducerPositions(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := "def first(value: i32 = 1) -> i32:\n    return value\n"
	_, old := plainDefaultProductFixture(t, source)
	_, fresh := plainDefaultProductFixture(t, "\n\n"+source)
	before, after := old.pre.plainHeaders["::first#0"], fresh.pre.plainHeaders["::first#0"]
	if before.Certificate == after.Certificate {
		t.Fatal("full coordinate certificate did not observe movement")
	}
	if !samePlainHeaderUnderSourceRebind(before, after) {
		t.Fatal("exact independent producer transition declined")
	}
	// Tampering with a typed payload, its producer reference, or its source receipt
	// cannot be authorized merely by recomputing the final digest.
	for _, kind := range []string{"payload", "producer", "source", "literal"} {
		t.Run(kind, func(t *testing.T) {
			bad := after
			bad.Parts = append([]plainHeaderCertificatePart(nil), after.Parts...)
			bad.SourcePositions = map[string]lexer.Pos{}
			for key, pos := range after.SourcePositions {
				bad.SourcePositions[key] = pos
			}
			index := -1
			for i, part := range bad.Parts {
				if part.IsPosition {
					index = i
					break
				}
			}
			if index < 0 {
				t.Fatal("fixture has no actual typed position contribution")
			}
			switch kind {
			case "payload":
				bad.Parts[index].Position.Offset++
			case "producer":
				bad.Parts[index].SourceField = "unowned.Position"
			case "source":
				pos := bad.SourcePositions[bad.Parts[index].SourceField]
				pos.Offset++
				bad.SourcePositions[bad.Parts[index].SourceField] = pos
			case "literal":
				bad.Parts[0].Text += "different-semantic-value"
			}
			var err error
			bad.Certificate, err = plainHeaderCertificateHash(bad.Parts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if samePlainHeaderUnderSourceRebind(before, bad) {
				t.Fatal("accepted forged complete producer receipt")
			}
		})
	}
	literal := strconv.Quote("\x00position:0\x00")
	parts, err := splitPlainHeaderCertificate(literal, nil)
	if err != nil || len(parts) != 1 || parts[0].Text != literal || parts[0].IsPosition {
		t.Fatal("quoted source material became typed authority")
	}
}

func TestPlainNoCallBodyCacheRetirementRequiresExactProducer(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := "def first(value: i32 = 1) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, source)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	state.a.callEdgeCache = map[*ast.FuncDecl][]*ast.FuncDecl{owner: nil}
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	if product.Rows["callEdgeCache"] != 1 || product.AuxiliaryRows["callEdgeCache"] != 0 {
		t.Fatal("actual empty producer was not encoded as owned row")
	}
	state.a.callEdgeCache[owner] = []*ast.FuncDecl{owner}
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("nonempty foreign call cache accepted for no-call source")
	}
}

func TestPlainFillAdoptionCacheRetirementUsesActualProducer(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := "def first(value: i32 = 1) -> i32:\n    return value\ndef second(value: i32 = 2) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, source)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	survivor := retainedProofNamedDecl(t, result, "second").(*ast.FuncDecl)
	// This is the same producer run by parallel precompute, not a planted false.
	state.a.fillMayAdoptSet = state.a.computeFillMayAdopt()
	if value, present := state.a.fillMayAdoptSet[owner]; !present || value {
		t.Fatal("actual closed-body classification is not false")
	}
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	if product.Rows["fillMayAdoptSet"] != 1 || product.AuxiliaryRows["fillMayAdoptSet"] != 0 {
		t.Fatal("exact classification row not inventoried")
	}
	if err := preflightRetainedPlainDefaultCleanup(state.a, product); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRetainedPlainDefaultProduct(state.a, state.result, product); err != nil {
		t.Fatal(err)
	}
	if _, present := state.a.fillMayAdoptSet[owner]; present {
		t.Fatal("removed owner remains in syntactic classifier")
	}
	if value, present := state.a.fillMayAdoptSet[survivor]; !present || value {
		t.Fatal("unrelated classifier row changed")
	}
}

func TestPlainFillAdoptionCacheRejectsDifferentProducerValue(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := "def first(value: i32 = 1) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, source)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	state.a.fillMayAdoptSet = state.a.computeFillMayAdopt()
	state.a.fillMayAdoptSet[owner] = true
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("non-source fill-adoption value silently accepted")
	}
}
