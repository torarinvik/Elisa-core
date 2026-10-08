package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

func TestRunCLIStdPrintRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel, err := filepath.Rel(fixtureDir, filepath.Join(std, "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("rel include: %v", err)
	}
	src := fmt.Sprintf(`# include %q

struct Label:
	text: cstr

impl Str for Label:
	def __cast__(self: Label) -> cstr can[Memory.Allocate, Console.Format, Abort.Panic]:
		return self.text

# Label has a second, unrelated target conversion. Generic string formatting
# must still select the Str protocol's cstr conversion from the requested target.
def __cast__(value: Label) -> i64:
	return 73

def main() -> int can[Console.Write]:
	printr("raw") can Console.Write
	print(" line") can Console.Write
	print(true) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	printr(false) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(" bool") can Console.Write
	label: Label = Label{text: "tagged"}
	print(label) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	printr(label) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(" after") can Console.Write
	print(label.i64()) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print('Z') can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(42) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(-9) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(7) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(99) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(5.usize()) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(3.5) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(2.25) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	print(sview("view", 0, -1)) can Memory.Allocate, Console{Format,Write}, Abort.Panic, Global{Read,Write}, Atomics{Exchange,Store}
	region scratch:
		in scratch:
			d: mutable dstr = ['d'.u8(), 'y'.u8(), 'n'.u8()]
			printr(d) can Console.Write
			print(" tail") can Console.Write
			print(d) can Console.Write
	return 0
`, filepath.ToSlash(rel))
	fixturePath := filepath.Join(fixtureDir, "std_print_smoke.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixturePath, map[string]bool{})
	if err != nil {
		t.Fatalf("expand print fixture includes: %v", err)
	}
	_, result, ok := analyzeProgram(fixturePath, expanded, &stderr)
	if !ok {
		t.Fatalf("analyze std print fixture failed:\n%s", stderr.String())
	}
	exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		t.Fatalf("build std print native fixture failed: %v\n%s", err, stderr.String())
	}
	defer cleanup()
	output, err := exec.Command(exePath).CombinedOutput()
	if err != nil {
		t.Fatalf("run std print native fixture failed: %v\n%s", err, string(output))
	}
	stdout := string(output)
	for _, want := range []string{
		"raw line\n",
		"true\n",
		"false bool\n",
		"tagged\n",
		"tagged after\n",
		"73\n",
		"Z\n",
		"42\n",
		"-9\n",
		"7\n",
		"99\n",
		"5\n",
		"3.5\n",
		"2.25\n",
		"view\n",
		"dyn tail\n",
		"dyn\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected stdout to contain %q, got stdout:\n%s\nstderr:\n%s", want, stdout, stderr.String())
		}
	}
}
