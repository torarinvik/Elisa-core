//go:build cgo

package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Partitioned IR at -O0: a private constant reached from functions owned by
// different partitions keeps one address, an edit to it is seen everywhere,
// and a signature edit reaches every caller.
func TestPartitionedIRPrivateConstantAndSignatureEdits(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	for revision, value := range []int{20, 21, 20} {
		var b strings.Builder
		fmt.Fprintf(&b, "const XS = [10, %d, 30]\n\n", value)
		for i := 0; i < 16; i++ {
			fmt.Fprintf(&b, "@inline(never)\ndef addr%d(index: usize) -> usize:\n    return (&XS[index]).cast[usize]\n\n", i)
		}
		b.WriteString("@inline(never)\ndef read(index: usize) -> i64:\n    return XS[index]\n\ndef main() -> i64:\n")
		for i := 1; i < 16; i++ {
			fmt.Fprintf(&b, "    if addr0(1) != addr%d(1):\n        return 1\n", i)
		}
		b.WriteString("    return read(1)\n")
		result := parseAndAnalyzeBackendTest(t, "partitioned_constant.elisa", b.String())
		before := session.Stats()
		out := filepath.Join(dir, "partitioned.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		if after := session.Stats(); after.SerialFallbacks != before.SerialFallbacks {
			t.Fatalf("revision%d did not use partitioned IR: %+v", revision, after)
		}
		if got := incrementalObjectNativeStatus(t, out); got != value {
			t.Fatalf("revision%d: got %d want %d", revision, got, value)
		}
	}
}

// Fresh sessions model separate CLI invocations: no semantic or object map is
// retained. Native behavior must change after an edit and match after reverting.
func TestPartitionedIRDiskCacheFreshSessions(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cache")
	for revision, value := range []int{20, 20, 21, 20} {
		session := NewIncrementalObjectSession(4, 8<<20)
		session.minWeight = 0
		session.EnableDiskCache(root, "test-compiler", 16<<20)
		var source strings.Builder
		source.WriteString("def keep[T](x: T) -> T:\n    return x\n\n")
		for i := 0; i < 80; i++ {
			fmt.Fprintf(&source, "@inline(never)\ndef f%d() -> i64:\n    return keep[i64](%d)\n\n", i, i)
		}
		fmt.Fprintf(&source, "def main() -> i64:\n    return f0() + %d\n", value)
		result := parseAndAnalyzeBackendTest(t, "disk_partitions.elisa", source.String())
		out := filepath.Join(dir, "out.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		paths, err := filepath.Glob(filepath.Join(session.disk.root, "*.schedule"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("missing emitted-membership record: %v %v", paths, err)
		}
		data, err := os.ReadFile(paths[0])
		var members [][]string
		if err != nil || len(data) < 32 || json.Unmarshal(data[32:], &members) != nil {
			t.Fatal("invalid emitted-membership record", err)
		}
		found := false
		for _, names := range members {
			for _, name := range names {
				found = found || name == "keep"
			}
		}
		if !found {
			t.Fatal("generic bodies missing from persisted scheduling record")
		}
		stats := session.Stats()
		if stats.SerialFallbacks != 0 {
			t.Fatalf("not partitioned: %+v", stats)
		}
		if revision > 0 && stats.DiskPartitionsReused == 0 {
			t.Fatalf("fresh process did not reuse disk partitions, revision %d: %+v", revision, stats)
		}
		if got := incrementalObjectNativeStatus(t, out); got != value {
			t.Fatalf("revision %d: native status %d want %d", revision, got, value)
		}
	}
}
