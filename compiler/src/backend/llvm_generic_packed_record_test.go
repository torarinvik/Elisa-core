//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenericPackedRecordUnalignedNativeAccess(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "packed-native.elisa", `struct Record[T] layout(packed):
    tag: u8
    payload: mutable T

def read(value: Record[i64]&) -> i64:
    return value.payload
def write(value: mutable Record[i64]&, payload: i64) -> void:
    value.payload <- payload
export fn read(value: Record[i64]&) -> i64 = read
export fn write(value: mutable Record[i64]&, payload: i64) -> void = write
`)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ir, "= type <{ i8, i64 }>") {
		t.Fatalf("generic packed instance lost packed layout:\n%s", ir)
	}
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
