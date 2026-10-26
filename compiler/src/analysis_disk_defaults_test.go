package main

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

func TestAnalysisDiskDefaultNativePipelineFreshProcess(t *testing.T) {
	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT", "ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS", "ELISACORE_OBJECT_PARTITIONS", "ELISACORE_PARALLEL_OPT_IMPORT"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	var text strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&text, "def work%d(value: i64) -> i64:\n    return value + %d\n\n", i, i+1)
	}
	text.WriteString("def main() -> i64:\n    return work0(20) + work79(1)\n")
	base := text.String()
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_BUILD_TIMING": "1", "ELISACORE_RETAINED_SERVER": ""}
	for index, body := range []string{base, strings.Replace(base, "return value + 1\n", "return value + 2\n", 1)} {
		if err := os.WriteFile(source, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		object := filepath.Join(dir, "program.o")
		output := runDiskCLIChild(t, env, "-emit", "obj", "-o", object, source)
		if !strings.Contains(output, "[build-timing] optimized module disk miss:") || strings.Contains(output, "[build-timing] partitioned object:") {
			t.Fatal("default disk request did not select whole-module O3", output)
		}
		if index == 1 && (!strings.Contains(output, "[analysis-disk] restore") || strings.Contains(output, "cold analysis")) {
			t.Fatal("edit did not replay disk state", output)
		}
		executable := filepath.Join(dir, "program")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "clang", object, "-o", executable).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link %v %s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = exec.CommandContext(ctx, executable).Run()
		cancel()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 102+index {
			t.Fatalf("revision %d native status: %v", index, err)
		}
	}
}
