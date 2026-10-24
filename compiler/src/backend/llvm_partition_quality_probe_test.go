//go:build cgo

package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Optional diagnostic: compares code quality, not just identical exit status.
// Run the test binary externally under a 30-second cap, without other builds.
func TestPartitionInliningQualityProbe(t *testing.T) {
	root := os.Getenv("ELISA_PARTITION_QUALITY_OUTPUT")
	if root == "" {
		t.Skip("explicit diagnostic output required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	leaf := ""
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("quality_leaf%d", i)
		if partitionOwner(candidate, 4) != partitionOwner("fold", 4) {
			leaf = candidate
			break
		}
	}
	if leaf == "" {
		t.Fatal("no cross-partition leaf")
	}
	source := fmt.Sprintf(`def %s(value: i64) -> i64:
    return (value * 3 + 1) ^ (value >> 3)

def fold(count: i64, seed: i64) -> i64:
    total: mutable i64 = 0
    for index in 0..<count:
        total <- total + %s(index ^ seed)
    return total
export fn fold(count: i64, seed: i64) -> i64 = fold
`, leaf, leaf)
	client := `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
extern int64_t fold(int64_t,int64_t);
static double now(void){struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return t.tv_sec+t.tv_nsec*1e-9;}
int main(int argc,char **argv){
 int64_t n=argc>1?strtoll(argv[1],0,10):50000000,seed=argc+17;
 int64_t expected=0;for(int64_t i=0;i<n;i++){int64_t x=i^seed;expected+=((x*3+1)^(x>>3));}
 double times[5];int64_t result=0;
 for(int i=0;i<5;i++){double start=now();result=fold(n,seed);times[i]=now()-start;if(result!=expected)return 91;}
 printf("{\"checksum\":%lld,\"seconds\":[%.9f,%.9f,%.9f,%.9f,%.9f]}\n",(long long)result,times[0],times[1],times[2],times[3],times[4]);return 0;
}`
	path := filepath.Join(root, "client.c")
	if err := os.WriteFile(path, []byte(client), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"whole-module", "independent-ir", "independent-ir-imports"} {
		t.Setenv("ELISACORE_IR_IMPORTS", "0")
		if mode == "independent-ir-imports" {
			t.Setenv("ELISACORE_IR_IMPORTS", "1")
		}
		t.Setenv("ELISACORE_PARALLEL_IR", "0")
		t.Setenv("ELISACORE_PARALLEL_OPT", "0")
		t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "0")
		if mode != "whole-module" {
			t.Setenv("ELISACORE_PARALLEL_IR", "1")
			t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		}
		result := parseAndAnalyzeBackendTest(t, "quality-probe.elisa", source)
		session := NewIncrementalObjectSession(4, 32<<20)
		session.minWeight = 0
		object := filepath.Join(root, mode+".o")
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: mode == "whole-module"}); err != nil {
			t.Fatal(err)
		}
		if mode != "whole-module" && session.Stats().OwnerMapBuilds != 0 {
			t.Fatal("independent path declined")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		binary := filepath.Join(root, mode)
		out, err := exec.CommandContext(ctx, "clang", path, object, "-O3", "-o", binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v %s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		out, err = exec.CommandContext(ctx, binary, "50000000").CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("run: %v %s", err, out)
		}
		var record struct {
			Checksum int64     `json:"checksum"`
			Seconds  []float64 `json:"seconds"`
		}
		if err := json.Unmarshal(out, &record); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, mode+".json"), out, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s leaf=%s stats=%+v runtime=%s", mode, leaf, session.Stats(), out)
	}
}
