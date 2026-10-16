package semantic

import (
	"strings"
	"testing"
)

// docs/128: an untyped callee body still types a field chain rooted at an explicitly typed
// local (`action: Action = s.deferred[i]`), and `extend` of a darray whose declared elements
// are program-lived copies only program-region views. Before, `c.names.extend(action.names)`
// was judged an element store of the caller's local Scope and the call was rejected.
func TestStoreScanTypedLocalExtendOfProgramLivedRow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typed_local_extend_ok.elisa", `global region source

struct Action:
    names: mutable darray[sview @source] = []
    scratch: mutable darray[sview] = []

struct Scope:
    deferred: mutable darray[Action] = []

struct Captured:
    names: mutable darray[sview @source] = []
    plain: mutable darray[sview] = []

def caller(c: mutable Captured&) -> void:
    can Memory.Allocate, Abort.Panic:
        s: Scope = Scope{}
        copy_names(c, s, 0)

def copy_names(c: mutable Captured&, s: Scope&, i: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        action: Action = s.deferred[i]
        c.names.extend(action.names)

def main() -> i32:
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, storeScanCallStore) {
		t.Fatalf("extend of a program-lived row was treated as a short-lived store:\n%s", all)
	}
}

// Negatives: extending from a plain-view row of the same typed local still stores the caller's
// short-lived views, into a plain row or into an @source row.
func TestStoreScanTypedLocalExtendOfPlainRowStillRejected(t *testing.T) {
	cases := map[string]string{
		"into_plain_row": `global region source

struct Action:
    names: mutable darray[sview @source] = []
    scratch: mutable darray[sview] = []

struct Scope:
    deferred: mutable darray[Action] = []

struct Captured:
    names: mutable darray[sview @source] = []
    plain: mutable darray[sview] = []

def caller(c: mutable Captured&) -> void:
    can Memory.Allocate, Abort.Panic:
        s: Scope = Scope{}
        copy_names(c, s, 0)

def copy_names(c: mutable Captured&, s: Scope&, i: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        action: Action = s.deferred[i]
        c.plain.extend(action.scratch)

def main() -> i32:
    return 0
`,
		"into_source_row": `global region source

struct Action:
    names: mutable darray[sview @source] = []
    scratch: mutable darray[sview] = []

struct Scope:
    deferred: mutable darray[Action] = []

struct Captured:
    names: mutable darray[sview @source] = []
    plain: mutable darray[sview] = []

def caller(c: mutable Captured&) -> void:
    can Memory.Allocate, Abort.Panic:
        s: Scope = Scope{}
        copy_names(c, s, 0)

def copy_names(c: mutable Captured&, s: Scope&, i: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        action: Action = s.deferred[i]
        c.names.extend(action.scratch)

def main() -> i32:
    return 0
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", src)
			all := strings.Join(result.Errors(), "\n")
			if !strings.Contains(all, storeScanCallStore) {
				t.Fatalf("expected the plain-view extend to be rejected, got:\n%s", all)
			}
		})
	}
}
