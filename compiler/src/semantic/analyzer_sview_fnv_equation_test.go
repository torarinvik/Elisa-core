package semantic

import (
	"strings"
	"testing"
)

func TestGhostSViewFNVRecursiveLocalsProveModelEquation(t *testing.T) {
	src := `const SVIEW_FNV1A_OFFSET_BASIS: u64 = 0xcbf29ce484222325
const SVIEW_FNV1A_PRIME: u64 = 0x100000001b3

ghost def sview_fnv1a_fold_from(value: sview, index: usize, remaining: usize, state: u64) -> u64:
    requires value.len > 0 and value.len.usize().i64() == value.len
    requires index <= value.len.usize()
    requires remaining == value.len.usize() - index
    decreases remaining
    ensure index == value.len.usize() implies result == state
    ensure index < value.len.usize() implies result == sview_fnv1a_fold_from(
        value, index + 1.usize(), remaining - 1.usize(),
        (state ^ value[index].u8().u64()) * SVIEW_FNV1A_PRIME
    )
    return state if remaining == 0.usize()
    byte: u8 = value[index].u8()
    next_state: u64 = (state ^ byte.u64()) * SVIEW_FNV1A_PRIME
    return sview_fnv1a_fold_from(value, index + 1.usize(), remaining - 1.usize(), next_state)

ghost def sview_fnv1a_model(value: sview) -> u64:
    ensure (value.len <= 0 or value.len.usize().i64() != value.len) implies result == SVIEW_FNV1A_OFFSET_BASIS
    ensure (value.len > 0 and value.len.usize().i64() == value.len) implies result == sview_fnv1a_fold_from(value, 0.usize(), value.len.usize(), SVIEW_FNV1A_OFFSET_BASIS)
    return SVIEW_FNV1A_OFFSET_BASIS if value.len <= 0 or value.len.usize().i64() != value.len
    return sview_fnv1a_fold_from(value, 0.usize(), value.len.usize(), SVIEW_FNV1A_OFFSET_BASIS)

ghost def model_matches_fold(value: sview) -> bool:
    requires value.len > 0 and value.len.usize().i64() == value.len
    ensure result
    return sview_fnv1a_model(value) == sview_fnv1a_fold_from(value, 0.usize(), value.len.usize(), SVIEW_FNV1A_OFFSET_BASIS)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_fnv_equation.elisa", src, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("production-shaped recursive FNV spec should prove through its immutable locals, got:\n%s\nproof report: %+v", strings.Join(errs, "\n"), result.ProofReport)
	}
	modelEquationProven := false
	for _, proof := range result.ProofReport {
		if proof.Subject == "ensure model_matches_fold" && proof.Predicate == "ensure" && proof.Outcome == ProofProvenSMT {
			modelEquationProven = true
			break
		}
	}
	if !modelEquationProven {
		t.Fatalf("expected the wrapper's exact model equation to be discharged by SMT, got: %+v", result.ProofReport)
	}
}
func TestSMTModelsSViewByteCharToU64WithoutWideningArbitraryChar(t *testing.T) {
	positive := `def view_byte_u64(view: sview, index: usize) -> u64:
    requires index < view.len.usize()
    ensure result >= 0
    ensure result < 256
    return view[index].u64()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_byte_to_u64.elisa", positive, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a stable SView byte widened to u64 should retain its byte range, got:\n%s", strings.Join(errs, "\n"))
	}

	negative := `def arbitrary_char_to_u64(value: char) -> u64:
    ensure result <= 9223372036854775807
    return value.u64()
`
	control := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "arbitrary_char_to_u64.elisa", negative, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if !strings.Contains(strings.Join(control.Errors(), "\n"), "could not be proven") {
		t.Fatalf("the char-to-u64 cast must not assume arbitrary char values fit i64's positive range, got: %v", control.Errors())
	}
}
