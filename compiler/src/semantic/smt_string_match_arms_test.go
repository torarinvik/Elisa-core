//go:build cgo

package semantic

import "testing"

// A string `when` ensure over a string `when` body whose arms compute non-literal booleans:
// the arm values are read-only, so each translates under its arm's string guard.
func TestSMTStringMatchNonLiteralArms(t *testing.T) {
	positive := `
const UA: u8 = 65
const UZ: u8 = 90

def pc(name: sview) -> bool can[Abort.Panic]:
    ensure when name:
        "void" | "bool" -> result == (true)
        _ -> result == (name.len > 0 and name[0] >= UA and name[0] <= UZ)
    can Abort.Panic:
        return when name:
            "void" | "bool" -> true
            _ -> name.len > 0 and name[0] >= UA and name[0] <= UZ

def pd(name: sview) -> bool can[Abort.Panic]:
    ensure when name:
        "void" | "bool" -> result == (true)
        _ -> result == (name.len > 0)
    can Abort.Panic:
        return when name:
            "void" | "bool" -> true
            _ -> name.len > 0

`
	result := analyzeContractStrict(t, "string_arms.elisa", positive)
	for _, name := range []string{"pc", "pd"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	// SOUNDNESS: a wrong default arm, a wrong literal arm, and a body arm the ensure disagrees with.
	negative := `
def p1(name: sview) -> bool can[Abort.Panic]:
    ensure when name:
        "void" | "bool" -> result == (true)
        _ -> result == (name.len > 1)
    can Abort.Panic:
        return when name:
            "void" | "bool" -> true
            _ -> name.len > 0

def p2(name: sview) -> bool can[Abort.Panic]:
    ensure when name:
        "void" -> result == (false)
        _ -> result == (name.len > 0)
    can Abort.Panic:
        return when name:
            "void" | "bool" -> true
            _ -> name.len > 0

def p3(name: sview) -> bool can[Abort.Panic]:
    ensure when name:
        "bool" -> result == (true)
        _ -> result == (name.len > 0)
    can Abort.Panic:
        return when name:
            "void" -> false
            _ -> name.len > 0

`
	result = analyzeContractStrict(t, "string_arms_bad.elisa", negative)
	for _, name := range []string{"p1", "p2", "p3"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}
