package main

import (
	"bytes"
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSourceSyntaxSuffixReplayMatchesFreshAndOwnsNodes(t *testing.T) {
	source := "def first(x: i64) -> i64:\n    return x + 1\n\nprivate def second() -> i64:\n    return 7\n\ndef last() -> i64:\n    return second()\n"
	cache := newSourceTokenCache(2, 8<<20)
	for i := 0; i < 2; i++ {
		if _, ok := parseProgramWithTokenCache("suffix.elisa", []byte(source), &bytes.Buffer{}, cache); !ok {
			t.Fatal("prime")
		}
	}
	for _, value := range []int{2, 3, 4, 5, 6, 7, 8, 9, 1} {
		changed := strings.Replace(source, "x + 1", fmt.Sprintf("x + %d", value), 1)
		before := cache.suffixParsedDeclarations
		var gotDiagnostics, wantDiagnostics bytes.Buffer
		got, ok := parseProgramWithTokenCache("suffix.elisa", []byte(changed), &gotDiagnostics, cache)
		want, freshOK := parseProgram("suffix.elisa", []byte(changed), &wantDiagnostics)
		if !ok || !freshOK || gotDiagnostics.String() != wantDiagnostics.String() || !sameSuffixSyntaxWithVisibility(got, want) {
			t.Fatalf("value=%d: suffix differs from fresh: %s / %s", value, &gotDiagnostics, &wantDiagnostics)
		}
		if value != 1 && cache.suffixParsedDeclarations-before != 2 {
			t.Fatalf("value=%d: no two-declaration suffix reuse", value)
		}
		// Later consumers rewrite declarations and visibility. The cache must stay pristine.
		got.Decls[1] = got.Decls[0]
		for key := range got.DeclVisibility {
			got.DeclVisibility[key] = "public"
		}
	}
}

func TestSourceSyntaxSuffixDeclinesShiftedPositionsAndErrors(t *testing.T) {
	source := "def first() -> i64:\n    return 1\n\ndef second() -> i64:\n    return 2\n"
	for _, changed := range []string{"\n" + source, strings.Replace(source, "return 1", "return 10", 1), strings.Replace(source, "return 1", "return )", 1)} {
		cache := newSourceTokenCache(4, 8<<20)
		for i := 0; i < 2; i++ {
			parseProgramWithTokenCache("shift.elisa", []byte(source), &bytes.Buffer{}, cache)
		}
		var gotDiagnostics, wantDiagnostics bytes.Buffer
		got, ok := parseProgramWithTokenCache("shift.elisa", []byte(changed), &gotDiagnostics, cache)
		want, freshOK := parseProgram("shift.elisa", []byte(changed), &wantDiagnostics)
		if ok != freshOK || gotDiagnostics.String() != wantDiagnostics.String() || !reflect.DeepEqual(got, want) {
			t.Fatalf("shift/error parity: %q", changed)
		}
		if cache.suffixParsedDeclarations != 0 {
			t.Fatal("reused shifted/error suffix")
		}
	}
}

func BenchmarkSourceSyntaxCacheEarlyEditSuffix(b *testing.B) {
	var source strings.Builder
	source.WriteString("def first(x: i64) -> i64:\n    return x + 100000\n\n")
	for i := 0; i < 512; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	original := source.String()
	for _, mode := range []string{"tokens", "suffix"} {
		b.Run(mode, func(b *testing.B) {
			cache := newSourceTokenCache(2, defaultSourceTokenCacheBytes)
			for i := 0; i < 2; i++ {
				parseProgramWithTokenCache("early.elisa", []byte(original), &bytes.Buffer{}, cache)
			}
			if mode == "tokens" {
				for key, entry := range cache.entries {
					entry.parsed = nil
					entry.trace = nil
					entry.parseDeclined = true
					cache.entries[key] = entry
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Equal-width numeric edits preserve all following source positions.
				changed := strings.Replace(original, "100000", fmt.Sprintf("%06d", 100001+i%899999), 1)
				if _, ok := parseProgramWithTokenCache("early.elisa", []byte(changed), &bytes.Buffer{}, cache); !ok {
					b.Fatal("parse")
				}
			}
			b.StopTimer()
			if mode == "suffix" && cache.suffixParsedDeclarations < uint64(512*b.N) {
				b.Fatal("no suffix reuse")
			}
		})
	}
}

func sameSuffixSyntaxWithVisibility(got, want *ast.File) bool {
	if got == nil || want == nil {
		return got == want
	}
	if len(got.DeclVisibility) != len(want.DeclVisibility) || len(got.Decls) != len(want.Decls) {
		return false
	}
	for i, decl := range got.Decls {
		if got.DeclVisibility[decl] != want.DeclVisibility[want.Decls[i]] {
			return false
		}
	}
	a, b := *got, *want
	a.DeclVisibility = nil
	b.DeclVisibility = nil
	return reflect.DeepEqual(a, b)
}

func TestSourceSyntaxSuffixConcurrentEditsOwnNodes(t *testing.T) {
	source := "def first() -> i64:\n    return 100000\n\ndef second() -> i64:\n    return 2\n\ndef third() -> i64:\n    return 3\n"
	cache := newSourceTokenCache(16, 8<<20)
	for i := 0; i < 2; i++ {
		parseProgramWithTokenCache("concurrent_suffix.elisa", []byte(source), &bytes.Buffer{}, cache)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			changed := strings.Replace(source, "100000", fmt.Sprint(100001+i), 1)
			var gotErrors, wantErrors bytes.Buffer
			got, ok := parseProgramWithTokenCache("concurrent_suffix.elisa", []byte(changed), &gotErrors, cache)
			want, wantOK := parseProgram("concurrent_suffix.elisa", []byte(changed), &wantErrors)
			if !ok || !wantOK || !reflect.DeepEqual(got, want) || gotErrors.String() != wantErrors.String() {
				t.Error("concurrent suffix parity")
				return
			}
			got.Decls[1].(*ast.FuncDecl).Body = nil
		}(i)
	}
	workers.Wait()
	if cache.suffixParsedDeclarations != 16 {
		t.Fatalf("reused %d suffix declarations, want 16", cache.suffixParsedDeclarations)
	}
}

func BenchmarkSourceSyntaxSuffixPaired(b *testing.B) {
	var text strings.Builder
	text.WriteString("def first(x: i64) -> i64:\n    return x + 100000\n\n")
	for i := 0; i < 512; i++ {
		fmt.Fprintf(&text, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	source := text.String()
	caches := []*sourceTokenCache{newSourceTokenCache(2, defaultSourceTokenCacheBytes), newSourceTokenCache(2, defaultSourceTokenCacheBytes)}
	for mode, cache := range caches {
		for i := 0; i < 2; i++ {
			parseProgramWithTokenCache("paired.elisa", []byte(source), &bytes.Buffer{}, cache)
		}
		if mode == 0 {
			for key, entry := range cache.entries {
				entry.parsed = nil
				entry.trace = nil
				entry.parseDeclined = true
				cache.entries[key] = entry
			}
		}
	}
	var elapsed [2]time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		changed := []byte(strings.Replace(source, "100000", fmt.Sprintf("%06d", 100001+i%899999), 1))
		for step := 0; step < 2; step++ {
			mode := (i + step) % 2
			start := time.Now()
			_, ok := parseProgramWithTokenCache("paired.elisa", changed, &bytes.Buffer{}, caches[mode])
			elapsed[mode] += time.Since(start)
			if !ok {
				b.Fatal("parse")
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(elapsed[0])/float64(b.N), "tokens-ns/edit")
	b.ReportMetric(float64(elapsed[1])/float64(b.N), "suffix-ns/edit")
	if caches[1].suffixParsedDeclarations < uint64(512*b.N) {
		b.Fatal("no replay")
	}
}
