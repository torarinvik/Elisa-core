package semantic

import (
	"strings"
	"testing"
)

// Phase 3b: a whole-family grant of a permission that `includes` another family
// satisfies a required member of the included family (the subsumption lattice).
func TestIncludingFamilyGrantSatisfiesIncludedMemberRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "includes_grant_satisfies.elisa", `
permission Disk:
    Read
    Write

permission IO:
    includes Disk

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can IO:
        return read_disk()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `read_disk`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected `can IO:` (IO includes Disk) to satisfy a Disk.Read call, got:\n%s", all)
	}
}

// Transitive subsumption: IO includes Disk, App includes IO ⇒ `can App:` satisfies Disk.Read.
func TestTransitiveIncludesGrantSatisfiesRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "includes_transitive.elisa", `
permission Disk:
    Read

permission IO:
    includes Disk

permission App:
    includes IO

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can App:
        return read_disk()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `read_disk`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected transitive `can App:` to satisfy a Disk.Read call, got:\n%s", all)
	}
}

// An unrelated family grant must NOT satisfy a requirement (no spurious subsumption).
func TestUnrelatedFamilyGrantDoesNotSatisfyRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "includes_unrelated.elisa", `
permission Disk:
    Read

permission Net:
    Connect

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can Net:
        return read_disk()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `read_disk`) {
		t.Fatalf("expected unrelated `can Net:` not to satisfy a Disk.Read call, got:\n%s", all)
	}
}

// `includes` of an undeclared family is reported.
func TestIncludesUnknownFamilyIsRejected(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "includes_unknown.elisa", `
permission IO:
    includes Ghost
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `includes unknown family "Ghost"`) {
		t.Fatalf("expected unknown-include diagnostic, got:\n%s", all)
	}
}

// Phase 4: a sound `can X as Y:` cast (Y subsumes X) discharges X in the body and
// surfaces Y as the function's inferred requirement.
func TestSoundCanCastReattributesToSupersetFamily(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "can_cast_sound.elisa", `
permission Disk:
    Read

permission IO:
    includes Disk

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can Disk.Read as IO:
        return read_disk()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `read_disk`) || strings.Contains(all, `explicit local effect grant`) || strings.Contains(all, `unsound`) {
		t.Fatalf("expected sound `as IO` cast to discharge the Disk.Read call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[IO]" {
		t.Fatalf("expected cast to surface IO, got %q", got)
	}
}

// An `as Y` where Y does not subsume X is rejected (and must suggest includes/trusted).
func TestUnsoundCanCastIsRejected(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "can_cast_unsound.elisa", `
permission Disk:
    Read

permission Net:
    Connect

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can Disk.Read as Net:
        return read_disk()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "`as Net` is unsound") {
		t.Fatalf("expected unsound-cast diagnostic, got:\n%s", all)
	}
}

// Alias-path subsumption: an `alias` capability set used as a `can` grant satisfies
// a narrower requirement — a wider capability caller satisfies a narrower callee.
// `alias IO = Read, Write` (via Cap.*) and `alias FileIO = IO, Seek`; a `can FileIO:`
// holder may call something requiring only `can[IO]`.
func TestWiderAliasGrantSatisfiesNarrowerRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "alias_subsumption_ok.elisa", `
permission Cap:
    Read
    Write
    Seek

alias IO = Cap.Read, Cap.Write
alias FileIO = IO, Cap.Seek

extern needs_io() -> i64 can[IO]

def caller() -> i64:
    can FileIO:
        return needs_io()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "needs_io") || strings.Contains(all, "explicit local effect grant") || strings.Contains(all, "unknown permission") {
		t.Fatalf("expected `can FileIO:` (FileIO includes IO) to satisfy a needs_io() requiring can[IO], got:\n%s", all)
	}
}

// An insufficient alias capability errors: a `can` holding only part of the
// requirement does not satisfy a call needing a member it lacks.
func TestInsufficientAliasGrantErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "alias_subsumption_insufficient.elisa", `
permission Cap:
    Read
    Write

alias ReadOnly = Cap.Read

extern needs_write() -> i64 can[Cap.Write]

def caller() -> i64:
    can ReadOnly:
        return needs_write()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "needs_write") {
		t.Fatalf("expected `can ReadOnly:` not to satisfy a Cap.Write requirement, got:\n%s", all)
	}
}

// Phase 4 (alias form): a sound `can X as <Alias>:` cast where the alias subsumes X
// discharges X locally and surfaces the alias as the function's requirement.
func TestSoundCanCastToSubsumingAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "can_cast_alias_sound.elisa", `
permission Cap:
    Read
    Write
    Seek

alias IO = Cap.Read, Cap.Write
alias FileIO = IO, Cap.Seek

extern read_io() -> i64 can[Cap.Read]

def build() -> i64:
    can Cap.Read as FileIO:
        return read_io()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "read_io") || strings.Contains(all, "explicit local effect grant") || strings.Contains(all, "unsound") {
		t.Fatalf("expected sound `as FileIO` cast to discharge the Cap.Read call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[FileIO]" {
		t.Fatalf("expected cast to surface FileIO, got %q", got)
	}
}

// An `as <Alias>` cast where the alias does NOT subsume X is rejected.
func TestUnsoundCanCastToAliasIsRejected(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "can_cast_alias_unsound.elisa", `
permission Cap:
    Read
    Write

permission Net:
    Connect

alias NetOnly = Net.Connect

extern read_io() -> i64 can[Cap.Read]

def build() -> i64:
    can Cap.Read as NetOnly:
        return read_io()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "`as NetOnly` is unsound") {
		t.Fatalf("expected unsound alias-cast diagnostic, got:\n%s", all)
	}
}

// Cyclic `includes` chains are rejected.
func TestIncludesCycleIsRejected(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "includes_cycle.elisa", `
permission A:
    includes B

permission B:
    includes A
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "cyclic `includes` chain") {
		t.Fatalf("expected include-cycle diagnostic, got:\n%s", all)
	}
}
