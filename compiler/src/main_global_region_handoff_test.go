package main

import (
	"bytes"
	"context"

	"elisacore/src/backend"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A by-value aggregate handed to a global must outlive its source region.
func TestGlobalRegionHandoffNative(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	dir := t.TempDir()
	input, object, binary := filepath.Join(dir, "input.elisa"), filepath.Join(dir, "input.o"), filepath.Join(dir, "input")
	source := `module G:
    public:
        struct T:
            xs: mutable darray[i64]
        global mutable saved: T? = null
        def publish(value: T?) -> void:
            can Global.Write:
                saved <- move value
        def read_back() -> i64:
            can Global.Read:
                if saved is held:
                    return held.xs[1]
            return -1
def main() -> i32:
    can Global.Write:
        region candidate_scope:
            items: darray[i64] = [5, 6, 7]
            candidate: G::T? = G::T{xs: items}
            G::publish(move candidate)
    can Global.Read:
        return 0 if G::read_back() == 6 else 1
`
	if err := os.WriteFile(input, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeObject := filepath.Join(dir, "runtime.o")
	var runtimeLog bytes.Buffer
	if err := writeDefaultElisaCoreRuntimeObject(runtimeObject, backend.DefaultPackedLoweringProfile(), "", &runtimeLog); err != nil {
		t.Fatalf("build runtime: %v\n%s", err, runtimeLog.String())
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI([]string{"-emit", "obj", opt, "-o", object, input}, &stdout, &stderr); code != 0 {
				t.Fatalf("compile: %d\n%s", code, stderr.String())
			}
			if output, err := compileNativeTestExecutable(t, clang, dir, []string{object, runtimeObject, "-o", binary}); err != nil {
				t.Fatalf("link: %v\n%s", err, output)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
				t.Fatalf("handoff lost its backing: %v\n%s", err, output)
			}
		})
	}
}
