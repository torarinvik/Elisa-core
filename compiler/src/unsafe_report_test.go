package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/easm"
	"elisacore/src/semantic"
)

func TestRunCLIEmitsUnsafeSummary(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "unsafe_summary.elisa")
	src := `
extern raw_cast(value: uintptr) -> heap u8& can[Unsafe.PointerCast]
extern raw_thread_share(value: heap u8&) -> void can[Unsafe.ThreadShare]
extern c_probe() -> i64

def safe_wrapper(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return value.cast[heap u8&]

def safe_ffi_wrapper() -> i64:
    trusted Unsafe.RawExtern:
        return c_probe()
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "unsafe", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"=== unsafe ===",
		"total: 5",
		"Unsafe.PointerCast: 1",
		"Unsafe.RawExtern: 3",
		"Unsafe.ThreadShare: 1",
		"boundary-invariants:",
		"GuestHostPointer:",
		"ThreadAffineSignalJump:",
		"c_probe: Unsafe.RawExtern",
		"raw_cast: Unsafe.PointerCast, Unsafe.RawExtern",
		"raw_thread_share: Unsafe.RawExtern, Unsafe.ThreadShare",
		"trusted-total: 2",
		"safe_ffi_wrapper: Unsafe.RawExtern",
		"safe_wrapper: Unsafe.PointerCast",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected unsafe report to contain %q, got:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{
		"functions:\n  safe_wrapper:",
		"functions:\n  safe_ffi_wrapper:",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("expected trusted wrapper implementation detail not to appear in surface function report, got:\n%s", output)
		}
	}
}

func TestRunCLIEnforcesUnsafeBudget(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "unsafe_budget.elisa")
	src := `
extern raw_cast(value: uintptr) -> heap u8& can[Unsafe.PointerCast]

def safe_wrapper(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return value.cast[heap u8&]
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "unsafe", "-unsafe-budget", "trusted-total=1,Unsafe.PointerCast=2", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected unsafe budget to pass, exit=%d\nstderr:\n%s", exitCode, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "unsafe", "-unsafe-budget", "trusted-total=0", fixturePath}, &stdout, &stderr)
	if exitCode == 0 || !strings.Contains(stderr.String(), "unsafe budget exceeded for trusted-total: 1 > 0") {
		t.Fatalf("expected unsafe budget failure, exit=%d\nstderr:\n%s", exitCode, stderr.String())
	}
}

func TestUnsafeSummaryIncludesEASMExportsAndRequires(t *testing.T) {
	report := generateUnsafeReport(&semantic.Result{
		GlobalScope: semantic.NewScope(nil),
		EASMModules: []*easm.Module{
			{
				Name: "asmguard",
				Functions: []easm.Function{
					{Name: "easm_load_fs", Requires: []string{"x86_64.segment.fs", "memory.raw_read"}},
				},
			},
		},
	})
	for _, want := range []string{
		"Unsafe.Assembly: 1",
		"EASM.Requires.memory.raw_read: 1",
		"EASM.Requires.x86_64.segment.fs: 1",
		"ExecutableCodePublish:",
		"MachineSegmentState:",
		"easm:easm_load_fs: EASM.Requires.memory.raw_read, EASM.Requires.x86_64.segment.fs, Unsafe.Assembly",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected unsafe report to contain %q, got:\n%s", want, report)
		}
	}
}

func TestUnsafeSummaryReportsBoundaryInvariantTrinity(t *testing.T) {
	report := generateUnsafeReport(&semantic.Result{
		GlobalScope: semantic.NewScope(nil),
		EASMModules: []*easm.Module{
			{
				Name: "trinity",
				Functions: []easm.Function{
					{Name: "easm_indirect_call", Requires: []string{"control.indirect"}},
				},
			},
		},
	})
	for _, want := range []string{
		"boundary-invariants:",
		"ExecutableCodePublish:",
		"static: runtime executable code must flow through a named publish primitive before call/jump",
		"trace: trace publish address, size, protection, cache/publish result, and first execution",
		"runtime: debug/referee mode halts execution of unpublished generated code",
		"TinyCallable:",
		"runtime: debug/referee mode halts poison, non-canonical, or near-null call/jump targets",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected unsafe report to contain %q, got:\n%s", want, report)
		}
	}
}

func TestUnsafeSummaryCountsEASMTinyTargetEscapeHatch(t *testing.T) {
	report := generateUnsafeReport(&semantic.Result{
		GlobalScope: semantic.NewScope(nil),
		EASMModules: []*easm.Module{
			{
				Name: "tiny",
				Functions: []easm.Function{
					{Name: "easm_sentinel_jump", Requires: []string{"control.indirect", "control.tiny_target.unchecked"}},
				},
			},
		},
	})
	for _, want := range []string{
		"Unsafe.TinyPointerSentinel: 1",
		"EASM.Requires.control.tiny_target.unchecked: 1",
		"easm:easm_sentinel_jump: EASM.Requires.control.indirect, EASM.Requires.control.tiny_target.unchecked, Unsafe.Assembly, Unsafe.TinyPointerSentinel",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected unsafe report to contain %q, got:\n%s", want, report)
		}
	}
}

func TestUnsafeSummaryCountsEASMPoisonTargetEscapeHatch(t *testing.T) {
	report := generateUnsafeReport(&semantic.Result{
		GlobalScope: semantic.NewScope(nil),
		EASMModules: []*easm.Module{
			{
				Name: "poison",
				Functions: []easm.Function{
					{Name: "easm_poison_jump_test", Requires: []string{"control.indirect", "control.poison_target.unchecked"}},
				},
			},
		},
	})
	for _, want := range []string{
		"Unsafe.PoisonPointerSentinel: 1",
		"EASM.Requires.control.poison_target.unchecked: 1",
		"easm:easm_poison_jump_test: EASM.Requires.control.indirect, EASM.Requires.control.poison_target.unchecked, Unsafe.Assembly, Unsafe.PoisonPointerSentinel",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected unsafe report to contain %q, got:\n%s", want, report)
		}
	}
}
