package semantic

import (
	"strings"
	"testing"
)

// Inside `module M:`, a bare call means M's own member -- that is what a namespace is
// for. Identifier resolution reaches the global scope through the ordinary scope chain,
// so a same-named STDLIB GENERIC was found there and returned before the enclosing
// module was consulted:
//
//	argument 1 to "add" expects mutable Flags[T]&, got mutable Box.Sack&
//
// The self-hosted stage1 already preferred the enclosing module's member, so the two
// compilers disagreed about a module member calling its own sibling by its own name.
//
// The collision has to be with a name the stdlib defines GENERICALLY and whose signature
// does not match: a module member named `add` taking two i64s resolves correctly even
// without the fix, so a test using that shape guards nothing. `add[T](mutable Flags[T]&, T)`
// is the real collision, and a ref parameter is what makes it bite.
func TestModuleSiblingCallBeatsStdlibGeneric(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "module_sibling_stdlib.elisa", `struct Flags[T]:
    bits: u64

def add[T](items: mutable Flags[T]&, value: T) -> void:
    items.bits <- items.bits | 1

module Box:
    struct Sack:
        items: mutable darray[i64]

    def add(s: mutable Sack&, v: i64) -> void:
        s.items.push(v)

    def fill(s: mutable Sack&) -> void:
        add(s, 7)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "Flags") {
		t.Fatalf("a module member's bare call must bind to its own sibling, not the generic; got: %s", all)
	}
}

// The preference must not reach outside the enclosing module, and must not disturb
// top-level resolution.
func TestModuleSiblingPreferenceIsScopedToTheEnclosingModule(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "module_sibling_scope.elisa", `def helper(v: i64) -> i64:
    return v * 10

module Other:
    def helper(v: i64) -> i64:
        return 99

module Box:
    def use() -> i64:
        return helper(3)

def top() -> i64:
    return helper(3)
`)
	if all := strings.Join(res.Errors(), "\n"); all != "" {
		t.Fatalf("a module with no member of the name must still see the top-level one; got: %s", all)
	}
}
