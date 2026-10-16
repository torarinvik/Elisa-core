//go:build cgo

package semantic

import "testing"

// These contracts are deliberately false for independent inputs. The SMT
// lowering must keep source names, field paths, and synthesized terms in
// separate namespaces; otherwise a false contract can be proved by symbol
// aliasing. Strict contract analysis is required because parseAndAnalyze does
// not enable ensure checking.
func requireSMTEnsureRejected(t *testing.T, source, filename, function string) {
	t.Helper()
	result := analyzeContractStrict(t, filename, source)
	if failures := ensureFailuresFor(result.Errors(), function); failures == 0 {
		t.Fatalf("expected the false ensure for %q to be rejected, got errors: %v", function, result.Errors())
	}
}

func TestSMTSymbolCollisionLocalAndFieldProjection(t *testing.T) {
	const prefix = `struct P:
	flag: bool

`
	const bad = prefix + `def bad(p: P, p__field__flag: bool) -> bool:
	ensure result == p.flag
	return p__field__flag
`
	const control = prefix + `def control(p: P, other: bool) -> bool:
	ensure result == p.flag
	return other
`
	requireSMTEnsureRejected(t, bad, "smt_local_field_collision.elisa", "bad")
	requireSMTEnsureRejected(t, control, "smt_local_field_collision_control.elisa", "control")
}

func TestSMTSymbolCollisionDistinctFieldPaths(t *testing.T) {
	const prefix = `struct Root1:
	a__field__b: bool

struct Root2:
	b: bool

`
	const bad = prefix + `def bad(x: Root1, x__field__a: Root2) -> bool:
	ensure result == x.a__field__b
	return x__field__a.b
`
	const control = prefix + `def control(x: Root1, other: Root2) -> bool:
	ensure result == x.a__field__b
	return other.b
`
	requireSMTEnsureRejected(t, bad, "smt_projection_path_collision.elisa", "bad")
	requireSMTEnsureRejected(t, control, "smt_projection_path_collision_control.elisa", "control")
}

func TestSMTSymbolCollisionSyntheticReturnAndSourceName(t *testing.T) {
	const prefix = `def give_false() -> bool:
	ensure result == false
	return false

`
	const bad = prefix + `def caller(__return_call_result: bool) -> bool:
	ensure result == __return_call_result
	return give_false()
`
	const control = prefix + `def caller(other: bool) -> bool:
	ensure result == other
	return give_false()
`
	requireSMTEnsureRejected(t, bad, "smt_synthetic_return_collision.elisa", "caller")
	requireSMTEnsureRejected(t, control, "smt_synthetic_return_collision_control.elisa", "caller")
}

func TestSMTSymbolCollisionArrayLengthAndSourceName(t *testing.T) {
	const bad = `def bad(xs: darray[i32, row], xs$len: i64) -> bool:
	ensure result == (xs.count == xs$len)
	return true
`
	const control = `def control(xs: darray[i32, row], other: i64) -> bool:
	ensure result == (xs.count == other)
	return true
`
	requireSMTEnsureRejected(t, bad, "smt_array_length_collision.elisa", "bad")
	requireSMTEnsureRejected(t, control, "smt_array_length_collision_control.elisa", "control")
}
