//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/125 §9 Tier 2 — a `machine over` whose input is a closed const enum must spell every
// variant as an explicit tag; an unguarded `_` is rejected and a missing variant errors.
const coverageMachinePreamble = `const enum Cls of u8:
    A
    B
    C

def classify(c: char) -> Cls:
    return when c:
        'a' -> Cls.A
        'b' -> Cls.B
        _ -> Cls.C

struct Cur:
    data: darray[char]
    pos: mutable usize

def at(cur: Cur&) -> char:
    return cur.data[cur.pos]

def more(cur: Cur&) -> bool:
    return cur.pos < cur.data.count

`

func coverageDiag(t *testing.T, name, machineBody string) string {
	t.Helper()
	src := coverageMachinePreamble + "def scan(cur: mutable Cur) -> i64:\n" + machineBody + "    return cur.pos.i64()\n"
	return allDiagnostics(analyzeTreeTestSourceWithSemanticErrors(t, name, src))
}

// All variants spelled, no wildcard → clean.
func TestMachineCoverageClosedEnumComplete(t *testing.T) {
	all := coverageDiag(t, "cov_ok.elisa", `    machine over classify(cur.at()) while cur.more():
        state Go
        start Go
        Go, Cls.A | Cls.B:
            cur.pos <- cur.pos + 1
            -> Go
        Go, Cls.C:
            break
`)
	if strings.Contains(all, "does not cover") || strings.Contains(all, "wildcard") {
		t.Fatalf("complete closed-enum machine must be clean, got:\n%s", all)
	}
}

// A missing variant is a hard error naming it.
func TestMachineCoverageClosedEnumMissingTag(t *testing.T) {
	all := coverageDiag(t, "cov_missing.elisa", `    machine over classify(cur.at()) while cur.more():
        state Go
        start Go
        Go, Cls.A | Cls.B:
            cur.pos <- cur.pos + 1
            -> Go
`)
	if !strings.Contains(all, "does not cover all variants") || !strings.Contains(all, "Cls.C") {
		t.Fatalf("expected missing-variant error naming Cls.C, got:\n%s", all)
	}
}

// A `_` wildcard over a closed enum is rejected — spell the tags.
func TestMachineCoverageClosedEnumWildcardRejected(t *testing.T) {
	all := coverageDiag(t, "cov_wild.elisa", `    machine over classify(cur.at()) while cur.more():
        state Go
        start Go
        Go, Cls.A:
            cur.pos <- cur.pos + 1
            -> Go
        Go, _:
            break
`)
	if !strings.Contains(all, "wildcard") || !strings.Contains(all, "spell the variants") {
		t.Fatalf("expected closed-enum wildcard rejection, got:\n%s", all)
	}
}
