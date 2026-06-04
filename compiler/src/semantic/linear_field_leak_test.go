package semantic

import (
	"strings"
	"testing"
)

// A `linear` (must-consume) value stored in a plain-struct FIELD must still carry its
// must-consume obligation. Previously protocolLiveLeafPaths gated aggregate descent on
// containsProtocolLeakValues, which only recognized builtin Thread/Task/MutexGuard
// handles — so a user `linear struct` nested in a plain struct silently lost its
// obligation (you could wrap a lock/transaction in a struct and drop it).
func TestLinearValueInStructFieldMustBeConsumed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "linear_field_leak.elisa", `linear struct Guard:
    open: bool
def make() -> Guard:
    return Guard(true)
struct Holder:
    g: Guard
def f() -> void:
    h: mutable Holder = Holder(make())
    return
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be consumed") {
		t.Fatalf("expected linear field h.g to require consumption; got:\n%s", all)
	}
}

// Control: a droppable `affine` struct field carries NO must-consume obligation, so
// wrapping it and dropping the holder must NOT be flagged (no false positive).
func TestDroppableAffineFieldNotFlagged(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "affine_field_ok.elisa", `affine struct Tok:
    n: i64
struct Holder:
    g: Tok
def f() -> void:
    h: mutable Holder = Holder(Tok(1))
    return
`)
	if all := strings.Join(result.Errors(), "\n"); strings.Contains(all, "must be consumed") {
		t.Fatalf("droppable affine field must NOT require consumption; got:\n%s", all)
	}
}
