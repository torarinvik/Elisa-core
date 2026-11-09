package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProjectEASMLintReportsProjectAndDependencySources(t *testing.T) {
	t.Parallel()
	projectRoot := writeProjectFixture(t, projectFixtureOptions{})
	if err := os.MkdirAll(filepath.Join(projectRoot, "easm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "lib", "mathcore.elisalib", "easm"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "include-dirs": ["shared"],
  "easm": ["easm/spin.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "run-emit": "interpret"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", manifestFileName), `{
  "provides": "mathcore",
  "entry": "src/mathcore.elisa",
  "interface": "src/mathcore.elisai",
  "include-dirs": ["shared"],
  "easm": ["easm/clock.easm"]
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "spin.easm"), `module spin
target any
export def easm_spin_pause() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "easm", "clock.easm"), `module clock
target any
export def easm_debug_trap() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: noreturn
    requires: debug.trap
    body:
        trap
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "easm-lint", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected easm-lint to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"EASM files (2)", "easm_spin_pause", "easm_debug_trap"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected easm-lint output to contain %q, got:\n%s", check, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "deps", "app", "--project", projectRoot, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deps to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"easm"`) || !strings.Contains(stdout.String(), "spin.easm") || !strings.Contains(stdout.String(), "clock.easm") {
		t.Fatalf("expected deps json to include easm sources, got:\n%s", stdout.String())
	}
}

func TestRunCLIProjectBuildEmitsEASMWrapper(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/identity.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "target-triple": "x86_64-apple-darwin"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern easm_identity(value: i64) -> i64

def main() -> int:
    return easm_identity(7).int()
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "identity.easm"), `module identity
target any
export def easm_identity(value: i64) -> i64 abi c:
    inputs: value = rdi
    outputs: ret = rax
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rax
        ret
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM project build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"define i64 @easm_identity", "asm sideeffect", "call i64 @easm_identity"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, stdout.String())
		}
	}
	for _, bad := range []string{"~{rax}", "~{rdi}"} {
		if strings.Contains(stdout.String(), bad) {
			t.Fatalf("expected fixed EASM input/output registers not to be repeated as clobbers via %q, got:\n%s", bad, stdout.String())
		}
	}
}

func TestRunCLIProjectBuildEmitsShadPS4StyleEASMWrappers(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/shadps4_low.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern shadps4_stack_pointer() -> uintptr
extern shadps4_fenced_rdtsc() -> u64
extern shadps4_spin_pause() -> void
extern shadps4_debug_trap() -> void

def main() -> int:
    shadps4_spin_pause()
    _ = shadps4_stack_pointer()
    _ = shadps4_fenced_rdtsc()
    return 0
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "shadps4_low.easm"), `module shadps4_low
target x86_64
export def shadps4_stack_pointer() -> uintptr abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rsp, %rax
        ret

export def shadps4_fenced_rdtsc() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, rdx, memory
    stack: unchanged
    control: returns
    requires: x86_64.rdtsc, x86_64.sse.lfence
    body:
        lfence
        rdtsc
        lfence
        ret

export def shadps4_spin_pause() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret

export def shadps4_debug_trap() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: noreturn
    requires: debug.trap
    body:
        trap
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected shadPS4-style EASM build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"define i64 @shadps4_stack_pointer",
		"define i64 @shadps4_fenced_rdtsc",
		"define void @shadps4_spin_pause",
		"define void @shadps4_debug_trap",
		"rdtsc",
		"pause",
		"trap",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIProjectBuildRequiresEASMEffectsOnExternSurface(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/guarded.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
	    }
	  }
	}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "guarded.easm"), `module guarded
target any
export def guarded_asm() -> void abi c can[Unsafe.Assembly]:
    stack: unchanged
    control: returns
    body:
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern guarded_asm() -> void

def main() -> int:
    guarded_asm()
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected EASM effects to require extern permissions, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "matching Elisa extern does not expose can[Unsafe.Assembly]") {
		t.Fatalf("expected missing Unsafe.Assembly diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern guarded_asm() -> void can[Unsafe.Assembly]

def main() -> int:
    can Unsafe.Assembly:
        guarded_asm()
    return 0
`)
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM extern permission bridge to build, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunCLIProjectBuildRequiresEASMSegmentMutationOnExternSurface(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/segment.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "segment.easm"), `module segment
target x86_64-apple-darwin
export def load_fs(selector: GuestFsSelector) -> void abi c:
    inputs: selector = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent
    body:
        movw %di, %fs
        state fs: guest
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern GuestFsSelectorRole
type GuestFsSelector = id[GuestFsSelectorRole]
const TEST_GUEST_FS_SELECTOR_ID: u32 = 1

extern load_fs(selector: GuestFsSelector) -> void

def main() -> int:
    load_fs(TEST_GUEST_FS_SELECTOR_ID.cast[GuestFsSelector])
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected EASM segment mutation to require extern permissions, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "matching Elisa extern does not expose can[Unsafe.SegmentMutation, Segment.Guest]") {
		t.Fatalf("expected missing Unsafe.SegmentMutation diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Segment.Guest") {
		t.Fatalf("expected missing Segment.Guest diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern GuestFsSelectorRole
type GuestFsSelector = id[GuestFsSelectorRole]
const TEST_GUEST_FS_SELECTOR_ID: u32 = 1

@segment_transition(guest)
extern load_fs(selector: GuestFsSelector) -> void can[Unsafe.SegmentMutation, Segment.Guest]

def main() -> int:
    can Unsafe.SegmentMutation, Segment.Guest:
        load_fs(TEST_GUEST_FS_SELECTOR_ID.cast[GuestFsSelector])
    return 0
`)
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM segment mutation extern permission to satisfy build, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
