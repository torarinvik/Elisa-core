package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strings"
	"testing"
)

const snapshotFactoryPrelude = `struct Snapshot[@r]:
    data: mutable darray[u8] @r
def make[@r]() -> Snapshot[r] @r:
    can Memory.Allocate:
        data: mutable darray[u8] @r = [65]
        return Snapshot[r]{data: data}
`

func TestFactoryOwnerRegionValueCollision(t *testing.T) {
	for _, name := range []string{"table", "target", "owner"} {
		t.Run(name, func(t *testing.T) {
			src := snapshotFactoryPrelude + fmt.Sprintf(`def caller[@%s](%s: mutable darray[Snapshot[%s]]& @%s) -> void:
    can Memory.Allocate, Abort.Panic:
        slots: mutable darray[Snapshot[%s]] @%s = [make[%s]()]
        %s.extend(slots)
def main() -> i64:
    return 0
`, name, name, name, name, name, name, name, name)
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_shadow.elisa", src, AnalyzeOptions{})
			if errs := r.Errors(); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
}
func TestFactoryOwnerShortRegionStillRejected(t *testing.T) {
	src := snapshotFactoryPrelude + `def caller[@target](target: mutable darray[Snapshot[target]]& @target) -> void:
    can Memory.Allocate, Abort.Panic:
        region short:
            slots: mutable darray[Snapshot[target]] @target = [make[short]()]
            target.extend(slots)
def main() -> i64:
    return 0
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_short.elisa", src, AnalyzeOptions{})
	if got := strings.Join(r.Errors(), "\n"); !strings.Contains(got, "freed first") {
		t.Fatalf("short owner must not escape: %s", got)
	}
}
func TestFactoryOwnerUnknownRegionStillRejected(t *testing.T) {
	src := snapshotFactoryPrelude + `def caller() -> void:
    can Memory.Allocate:
        _ = make[unknown]()
def main() -> i64:
    return 0
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_unknown.elisa", src, AnalyzeOptions{})
	if got := strings.Join(r.Errors(), "\n"); !strings.Contains(got, "must name a visible region") {
		t.Fatalf("unknown region must fail: %s", got)
	}
}
func TestFactoryOwnerSimultaneousRegionSubstitution(t *testing.T) {
	for _, names := range [][2]string{{"b", "c"}, {"b", "a"}, {"a", "b"}} {
		t.Run(names[0]+names[1], func(t *testing.T) {
			src := `struct Snapshot[@r]:
    data: mutable darray[u8] @r
def choose[@a, @b](left: darray[u8]& @a, right: darray[u8]& @b) -> Snapshot[a] @a:
    can Memory.Allocate:
        data: mutable darray[u8] @a = [65]
        return Snapshot[a]{data: data}
` + fmt.Sprintf(`def caller[@%s, @%s](left: darray[u8]& @%s, right: darray[u8]& @%s) -> void:
    can Memory.Allocate:
        _ = choose(left, right)
def main() -> i64:
    return 0
`, names[0], names[1], names[0], names[1])
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_alpha.elisa", src, AnalyzeOptions{})
			if errs := r.Errors(); len(errs) != 0 {
				t.Fatal(errs)
			}
			found := false
			for expr := range r.analyzer.exprTypes {
				if call, ok := expr.(*ast.CallExpr); ok && callIdentName(call) == "choose" {
					found = true
					inst, ok := r.analyzer.exprTypes[call].(*GenericInstanceType)
					if !ok || len(inst.Args) != 1 || typeString(inst.Args[0]) != names[0] {
						t.Fatalf("nominal result is %s, want Snapshot[%s]", typeString(r.analyzer.exprTypes[call]), names[0])
					}
					if got := r.analyzer.declaredCallReturnRegions[call]; got != names[0] {
						t.Fatalf("simultaneous owner became %q, want %q", got, names[0])
					}
					nominal := typeString(r.analyzer.exprTypes[call])
					t.Logf("nominal result=%s", nominal)
					want := "Snapshot[" + names[0] + "]"
					if !strings.Contains(nominal, want) {
						t.Fatalf("nominal result %q lost simultaneous owner %s", nominal, want)
					}
				}
			}
			if !found {
				t.Fatal("choose call missing")
			}
		})
	}
}
