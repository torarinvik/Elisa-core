//go:build cgo

package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternEnsureRuntimeCheckUsesCallArgument(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "extern_ensure_parameter.elisa", `
@callconv(c)
extern checked(value: i64) -> i64 ensure result == value

def caller(value: i64) -> i64:
    return checked(value)
export fn elisa_call(value: i64) -> i64 = caller
`)
	for _, tc := range []struct {
		name    string
		level   OptimizationLevel
		force   string
		checked bool
	}{
		{"checked O0", OptimizationLevel0, "", true},
		{"release O3", OptimizationLevel3, "", false},
		{"forced O3", OptimizationLevel3, "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ELISACORE_FORCE_CONTRACTS", tc.force)
			output, err := GenerateLLVMIRWithOpt(result, tc.level)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if got := strings.Contains(output, "extern postcondition failed"); got != tc.checked {
				t.Fatalf("extern runtime boundary check present=%v, want %v:\n%s", got, tc.checked, output)
			}
			if tc.checked && !strings.Contains(output, "icmp eq i64") {
				t.Fatalf("parameter-dependent equality check missing:\n%s", output)
			}
		})
	}
}

func TestExternEnsureCallArgumentsNative(t *testing.T) {
	t.Setenv("ELISACORE_FORCE_CONTRACTS", "1")
	const source = `@callconv(c)
extern checked(value: i64) -> i64 ensure result == value

def caller(value: i64) -> i64:
    return checked(value)
export fn elisa_call(value: i64) -> i64 = caller
`
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			dir := t.TempDir()
			object := filepath.Join(dir, "checked.o")
			result := parseAndAnalyzeBackendTest(t, "extern-ensure-native.elisa", source)
			if err := NewIncrementalObjectSession(4, 32<<20).WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: level, Serial: true}); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []int{0, 1} {
				client := filepath.Join(dir, "client.c")
				code := fmt.Sprintf("#include <stdlib.h>\nextern long long elisa_call(long long);\nlong long checked(long long value) { return value + %d; }\nvoid abort(void) { _Exit(73); }\nint main(void) { return elisa_call(7) != 7; }\n", bad)
				if err := os.WriteFile(client, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				binary := filepath.Join(dir, "client")
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				output, err := exec.CommandContext(ctx, "clang", object, client, "-o", binary).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("link: %v\n%s", err, output)
				}
				ctx, cancel = context.WithTimeout(context.Background(), 25*time.Second)
				output, err = exec.CommandContext(ctx, binary).CombinedOutput()
				cancel()
				status := 0
				if err != nil {
					exit, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatalf("run: %v\n%s", err, output)
					}
					status = exit.ExitCode()
				}
				want := 0
				if bad != 0 {
					want = 73
				}
				if status != want {
					t.Fatalf("extern result delta=%d: exit=%d, want=%d\n%s", bad, status, want, output)
				}
			}
		})
	}
}
