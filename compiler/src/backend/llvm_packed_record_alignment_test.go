//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPackedRecordFieldAccessUsesUnalignedMemory(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "packed-access.elisa", `struct Record layout(packed):
    tag: u8
    payload: mutable i64

def read(value: Record&) -> i64:
    return value.payload

def write(value: mutable Record&, payload: i64) -> void:
    value.payload <- payload
`)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{`load i64, ptr [^\n]+, align 1`, `store i64 [^\n]+, ptr [^\n]+, align 1`} {
		if !regexp.MustCompile(pattern).MatchString(ir) {
			t.Fatalf("missing unaligned field access %s:\n%s", pattern, ir)
		}
	}
}

func TestPackedRecordNestedAccessAlignment(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "packed-nested.elisa", `struct Inner:
    payload: mutable i64
struct Outer layout(packed):
    tag: u8
    inner: mutable Inner
    items: mutable array[i64, 2]
struct Ordinary:
    payload: mutable i64

def nested(value: Outer&) -> i64:
    return value.inner.payload + value.items[1]
def ordinary(value: Ordinary&) -> i64:
    return value.payload
`)
	for _, target := range []string{"arm64-apple-darwin", "x86_64-unknown-linux-gnu"} {
		ir, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), target)
		if err != nil {
			t.Fatal(err)
		}
		nested := ir[strings.Index(ir, "define i64 @nested"):strings.Index(ir, "define i64 @ordinary")]
		if len(regexp.MustCompile(`load i64, ptr [^\n]+, align 1`).FindAllString(nested, -1)) != 2 {
			t.Fatalf("target %s stale nested/array alignment:\n%s", target, ir)
		}
		ordinary := ir[strings.Index(ir, "define i64 @ordinary"):]
		if !regexp.MustCompile(`load i64, ptr [^\n]+, align 8`).MatchString(ordinary) {
			t.Fatalf("natural ordinary alignment weakened:\n%s", ir)
		}
	}
}

func TestPackedRecordUnalignedNativeAccess(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "packed-native.elisa", `struct Record layout(packed):
    tag: u8
    payload: mutable i64

def read(value: Record&) -> i64:
    return value.payload
def write(value: mutable Record&, payload: i64) -> void:
    value.payload <- payload
export fn read(value: Record&) -> i64 = read
export fn write(value: mutable Record&, payload: i64) -> void = write
`)
	dir := t.TempDir()
	client := filepath.Join(dir, "client.c")
	if err := os.WriteFile(client, []byte(`#include <stdint.h>
#include <stdlib.h>
#include <string.h>
typedef struct __attribute__((packed)) {uint8_t tag; int64_t payload;} Record;
extern int64_t read(Record*);
extern void write(Record*,int64_t);
int main(void) {
 unsigned char *allocation=malloc(4*sizeof(Record)+16);
 if(!allocation)return 99;
 Record *values=(Record*)(allocation+3);
 for(int i=0;i<4;i++){values[i].tag=(uint8_t)(i+1);write(&values[i],91234567890LL+i);}
 for(int i=0;i<4;i++)if(read(&values[i])!=91234567890LL+i || values[i].payload!=91234567890LL+i || values[i].tag!=i+1)return 17;
 free(allocation);return 0;
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		object := filepath.Join(dir, "record.o")
		if err := NewIncrementalObjectSession(4, 32<<20).WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: level, Serial: true}); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, "record")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "clang", object, client, "-O3", "-o", binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v %s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		out, err = exec.CommandContext(ctx, binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("native level %v: %v %s", level, err, out)
		}
	}
}
