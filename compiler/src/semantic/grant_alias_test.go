package semantic

import (
	"strings"
	"testing"
)

// A grant alias used in a local `can` block satisfies the member requirements it
// abbreviates, across families.
func TestGrantAliasSatisfiesCrossFamilyMembers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "grant_alias_ok.elisa", `
permission Zeg:
    Host
    Guest

permission Unsafe2:
    SegmentMutation

alias HostSeg = Zeg.Host, Unsafe2.SegmentMutation

extern host_write() -> i64 can[Zeg.Host]
extern mutate() -> i64 can[Unsafe2.SegmentMutation]

def build() -> i64:
    can HostSeg:
        _ = host_write()
        return mutate()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "host_write") || strings.Contains(all, "mutate") || strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected `can HostSeg:` to satisfy both member calls, got:\n%s", all)
	}
}

// The `alias` keyword is the canonical capability-set declaration (replacing
// `grant`); it behaves identically — used in a local `can` block it satisfies
// the member requirements it abbreviates.
func TestAliasKeywordCapabilitySet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "alias_keyword_ok.elisa", `
permission Zeg:
    Host
    Guest

permission Unsafe2:
    SegmentMutation

alias HostSeg = Zeg.Host, Unsafe2.SegmentMutation

extern host_write() -> i64 can[Zeg.Host]
extern mutate() -> i64 can[Unsafe2.SegmentMutation]

def build() -> i64:
    can HostSeg:
        _ = host_write()
        return mutate()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "host_write") || strings.Contains(all, "mutate") || strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected `can HostSeg:` (declared via `alias`) to satisfy both member calls, got:\n%s", all)
	}
}

// A grant alias may reference another grant alias (one level of nesting).
func TestGrantAliasNesting(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "grant_alias_nested.elisa", `
permission Zeg:
    Host

permission Mem:
    Allocate

alias Seg = Zeg.Host
alias SegMem = Seg, Mem.Allocate

extern host_write() -> i64 can[Zeg.Host]
extern alloc() -> i64 can[Mem.Allocate]

def build() -> i64:
    can SegMem:
        _ = host_write()
        return alloc()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "host_write") || strings.Contains(all, "alloc") || strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected nested grant alias to satisfy both calls, got:\n%s", all)
	}
}

// A grant alias is usable in an extern signature can[...] clause (it expands to
// its member refs), and a `can <alias>:` grant satisfies that requirement.
func TestGrantAliasInSignature(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "grant_alias_sig.elisa", `
permission Zeg:
    Host

alias HostSeg = Zeg.Host

extern needs_host() -> i64 can[HostSeg]

def build() -> i64:
    can HostSeg:
        return needs_host()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "unknown permission") || strings.Contains(all, "needs_host") || strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected grant alias to expand in both signature and grant, got:\n%s", all)
	}
}

// A grant alias that conflicts with a permission family name is rejected.
func TestGrantAliasConflictsWithPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "grant_alias_conflict.elisa", `
permission Zeg:
    Host

alias Segment = Zeg.Host
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "conflicts with a permission family") {
		t.Fatalf("expected conflict error, got:\n%s", all)
	}
}
