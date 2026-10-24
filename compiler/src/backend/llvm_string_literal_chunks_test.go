//go:build cgo && (darwin || linux)

package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStringLiteralChunksRespectNativeViewBounds(t *testing.T) {
	literals := []string{"", "a", "ab", "abc", "abcd", "abcde", "abcdef", "abcdefg", "abcdefgh", "abcdefghi", "a\x00b", "éxy", "\xff\x80z", "ab\x00éyz\xff"}
	var source, decls, checks, shims strings.Builder
	for i, text := range literals {
		fmt.Fprintf(&source, "def eq_%d(text: sview) -> bool:\n    return text == %s\n\n", i, strconv.Quote(text))
		fmt.Fprintf(&shims, "define i64 @probe_%d(ptr %%data, i64 %%len) {\n %%a = insertvalue %%StringView poison, ptr %%data, 0\n %%b = insertvalue %%StringView %%a, i64 %%len, 1\n %%c = call i1 @eq_%d(%%StringView %%b)\n %%v = zext i1 %%c to i64\n ret i64 %%v\n}\n", i, i)
		fmt.Fprintf(&decls, "extern int64_t probe_%d(const unsigned char*,int64_t);\n", i)
		fmt.Fprintf(&checks, "{static const unsigned char want[] = {")
		for _, b := range []byte(text) {
			fmt.Fprintf(&checks, "%d,", b)
		}
		fmt.Fprintf(&checks, "0}; if (check(probe_%d,want,%d)) return %d;}\n", i, len(text), i+1)
	}
	result := parseAndAnalyzeBackendTest(t, "literal_chunk_bounds.elisa", source.String())
	driver := `#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
typedef int64_t (*probe)(const unsigned char*,int64_t);
` + decls.String() + `
static int check(probe fn,const unsigned char *want,size_t n) {
 size_t page=(size_t)sysconf(_SC_PAGESIZE);
 unsigned char *area=mmap(0,page*2,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANON,-1,0);
 if(area==MAP_FAILED || mprotect(area+page,page,PROT_NONE)) return 1;
 // End exactly at a guard page; any overread must fault. Try every
 // unaligned offset separately, then perturb every byte of the input.
 for(size_t gap=0;gap<8;gap++) {
  unsigned char *data=area+page-n-gap;
  memcpy(data,want,n);
  if(!fn(data,(int64_t)n)) return 2;
  for(size_t j=0;j<n;j++) {
   data[j]^=0x80;
   if(fn(data,(int64_t)n)) return 3;
   data[j]^=0x80;
  }
  if(fn(data,(int64_t)n+1) || fn(data,-1)) return 4;
  if(n && fn(data,(int64_t)n-1)) return 5;
 }
 // Wrong lengths must not read even an inaccessible pointer; a null
 // nonempty view is rejected, and empty equality only consults length.
 if(fn(area+page,(int64_t)n+1) || fn(0,(int64_t)n+1)) return 6;
 if(fn(0,(int64_t)n) != (n==0)) return 7;
 munmap(area,page*2);
 return 0;
}
int main(void) {
` + checks.String() + `return 0;}
`
	dir := t.TempDir()
	cpath := filepath.Join(dir, "driver.c")
	if err := os.WriteFile(cpath, []byte(driver), 0600); err != nil {
		t.Fatal(err)
	}
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	irpath := filepath.Join(dir, "library.ll")
	if err := os.WriteFile(irpath, []byte(ir+"\n"+shims.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			executable := filepath.Join(dir, fmt.Sprintf("literal-%d", level))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, "clang", fmt.Sprintf("-O%d", level), cpath, "-x", "ir", irpath, "-o", executable).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			if out, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
				t.Fatalf("view boundary/equality: %v\n%s", err, out)
			}
		})
	}
}

func TestStringLiteralChunksUseTargetByteOrder(t *testing.T) {
	var source strings.Builder
	for i, pair := range [][2]string{{"ab\x00éyz", "ab\x00éyw"}, {"ab\x00éyz\xff", "ab\x00éyz\xfe"}} {
		for name, value := range map[string]string{"good": pair[0], "bad": pair[1]} {
			fmt.Fprintf(&source, "def %s%d() -> bool:\n    text: sview = %s\n    return text == %s\n\nexport fn %s%d() -> bool = %s%d\n\n", name, i, strconv.Quote(pair[0]), strconv.Quote(value), name, i, name, i)
		}
	}
	result := parseAndAnalyzeBackendTest(t, "literal_chunk_endian.elisa", source.String())
	for _, target := range []string{"x86_64-unknown-linux-gnu", "powerpc64-unknown-linux-gnu", "wasm32-unknown-unknown"} {
		t.Run(target, func(t *testing.T) {
			raw, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(raw, "svlit.chunk") {
				t.Fatal("fixture did not exercise literal loads")
			}
			optimized, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, OptimizationLevel3, DefaultPackedLoweringProfile(), target)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"good0": "ret i1 true", "bad0": "ret i1 false", "good1": "ret i1 true", "bad1": "ret i1 false"} {
				start := strings.Index(optimized, "@"+name+"(")
				if start < 0 {
					t.Fatal("missing function", name)
				}
				body := optimized[start:]
				end := strings.Index(body, "\n}")
				if end < 0 {
					t.Fatal("missing body", name)
				}
				if !strings.Contains(body[:end], want) {
					t.Fatalf("%s: target constant-folded equality incorrectly:\n%s", name, body[:end])
				}
			}
		})
	}
}
