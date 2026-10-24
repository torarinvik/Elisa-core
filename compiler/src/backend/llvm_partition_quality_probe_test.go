//go:build cgo

package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
 for(int warm=0;warm<5;warm++){result=fold(n,seed);if(result!=expected)return 91;}
 for(int i=0;i<5;i++){double start=now();result=fold(n,seed);times[i]=now()-start;if(result!=expected)return 91;}
 printf("{\"checksum\":%lld,\"seconds\":[%.9f,%.9f,%.9f,%.9f,%.9f]}\n",(long long)result,times[0],times[1],times[2],times[3],times[4]);return 0;
}`
	runPartitionQualityModes(t, root, source, client, "50000000", leaf)
}

func runPartitionQualityModes(t *testing.T, root, source, client, count, label string) {
	t.Helper()
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	previousWorkers := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	t.Cleanup(func() { partitionWorkersAvailable = previousWorkers })
	if err := os.WriteFile(filepath.Join(root, "source.elisa"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "client.c")
	if err := os.WriteFile(path, []byte(client), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"whole-module", "independent-ir", "independent-ir-imports", "disk-defaults"} {
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
		if mode == "disk-defaults" {
			unsetDiskPresetFlags(t)
			session.UseDiskIncrementalDefaults()
			session.EnableDiskCache(filepath.Join(root, "default-cache"), "quality-emitter", 64<<20)
		}
		object := filepath.Join(root, mode+".o")
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: mode == "whole-module"}); err != nil {
			t.Fatal(err)
		}
		if mode != "whole-module" && (session.Stats().OwnerMapBuilds != 0 || session.Stats().PartitionsEmitted == 0) {
			t.Fatal("independent path declined")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		binary := filepath.Join(root, mode)
		out, err := exec.CommandContext(ctx, "clang", path, object, "-O3", "-ffp-contract=off", "-o", binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v %s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, binary, count)
		if runtime.GOOS == "darwin" {
			command = exec.CommandContext(ctx, "/usr/bin/time", "-l", binary, count)
		}
		var counters bytes.Buffer
		command.Stderr = &counters
		out, err = command.Output()
		cancel()
		if writeErr := os.WriteFile(filepath.Join(root, mode+".time"), counters.Bytes(), 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
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
		t.Logf("%s leaf=%s stats=%+v runtime=%s", mode, label, session.Stats(), out)
	}
}

func TestPartitionBroaderQualityProbes(t *testing.T) {
	root := os.Getenv("ELISA_PARTITION_BROADER_QUALITY_OUTPUT")
	if root == "" {
		t.Skip("explicit diagnostic output required")
	}
	leaf := crossPartitionImportLeaf("fold")
	other := crossPartitionImportLeaf(leaf)
	common := `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static double now(void){struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return t.tv_sec+t.tv_nsec*1e-9;}
`
	cases := []struct {
		name, source, client, count string
		strict                      bool
	}{
		{"struct-alias", fmt.Sprintf(`struct Cell layout(c):
    value: mutable i64
    bias: i64

def %s(cell: Cell&) -> i64:
    return cell.value + cell.bias

def fold(cell: mutable Cell&, count: i64, seed: i64) -> i64:
    total: mutable i64 = 0
    for index in 0..<count:
        cell.value <- index ^ seed
        total <- total + %s(cell)
    return total
export fn fold(cell: mutable Cell&, count: i64, seed: i64) -> i64 = fold
`, leaf, leaf), common + `typedef struct {int64_t value,bias;} Cell;
extern int64_t fold(Cell*,int64_t,int64_t);
int main(int argc,char **argv){int64_t n=strtoll(argv[1],0,10),seed=argc+17,expected=0;for(int64_t i=0;i<n;i++)expected+=(i^seed)+7;
 Cell cell={0,7};double times[5];int64_t result=0;
 for(int warm=0;warm<5;warm++){result=fold(&cell,n,seed);if(result!=expected)return 91;}
 for(int i=0;i<5;i++){double start=now();result=fold(&cell,n,seed);times[i]=now()-start;if(result!=expected || cell.value!=((n-1)^seed) || cell.bias!=7)return 91;}
 printf("{\"checksum\":%lld,\"seconds\":[%.9f,%.9f,%.9f,%.9f,%.9f]}\n",(long long)result,times[0],times[1],times[2],times[3],times[4]);return 0;}
`, "30000000", false},
		{"strict-fp", fmt.Sprintf(`def %s(value: f64) -> f64:
    return value * 0.5 + 0.25

def fold(count: i64) -> f64:
    total: mutable f64 = 0.0
    for index in 0..<count:
        total <- total + %s((index %% 17).f64())
    return total
export fn fold(count: i64) -> f64 = fold
`, leaf, leaf), common + `extern double fold(int64_t);
int main(int argc,char **argv){int64_t n=strtoll(argv[1],0,10);double expected=0;for(int64_t i=0;i<n;i++)expected+=(double)(i%17)*0.5+0.25;
 uint64_t want,got;memcpy(&want,&expected,8);double times[5];
 for(int warm=0;warm<5;warm++){double result=fold(n);memcpy(&got,&result,8);if(got!=want)return 91;}
 for(int i=0;i<5;i++){double start=now();double result=fold(n);times[i]=now()-start;memcpy(&got,&result,8);if(got!=want)return 91;}
 printf("{\"checksum\":%llu,\"seconds\":[%.9f,%.9f,%.9f,%.9f,%.9f]}\n",(unsigned long long)got,times[0],times[1],times[2],times[3],times[4]);return 0;}
`, "20000000", true},
		{"mutual-recursion", fmt.Sprintf(`def %s(depth: i64, value: i64) -> i64:
    if depth == 0:
        return value
    return %s(depth - 1, (value * 3 + 1) & 65535)

def %s(depth: i64, value: i64) -> i64:
    if depth == 0:
        return value
    return %s(depth - 1, (value * 5 + 3) & 65535)

def fold(count: i64, seed: i64) -> i64:
    total: mutable i64 = 0
    for index in 0..<count:
        total <- total + %s(20, (index ^ seed) & 65535)
    return total
export fn fold(count: i64, seed: i64) -> i64 = fold
`, leaf, other, other, leaf, leaf), common + `extern int64_t fold(int64_t,int64_t);
int main(int argc,char **argv){int64_t n=strtoll(argv[1],0,10),seed=argc+17,expected=0;
 for(int64_t i=0;i<n;i++){int64_t x=(i^seed)&65535;for(int j=0;j<20;j++)x=(j%2==0?x*3+1:x*5+3)&65535;expected+=x;}
 double times[5];int64_t result=0;
 for(int warm=0;warm<5;warm++){result=fold(n,seed);if(result!=expected)return 91;}
 for(int i=0;i<5;i++){double start=now();result=fold(n,seed);times[i]=now()-start;if(result!=expected)return 91;}
 printf("{\"checksum\":%lld,\"seconds\":[%.9f,%.9f,%.9f,%.9f,%.9f]}\n",(long long)result,times[0],times[1],times[2],times[3],times[4]);return 0;}
`, "50000000", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ELISACORE_STRICT_FP", "")
			if c.strict {
				t.Setenv("ELISACORE_STRICT_FP", "1")
			}
			dir := filepath.Join(root, c.name)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			runPartitionQualityModes(t, dir, c.source, c.client, c.count, c.name)
		})
	}
}
