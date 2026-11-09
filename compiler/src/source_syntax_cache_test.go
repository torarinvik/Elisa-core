package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSourceSyntaxSnapshotAdmissionAndPrivateReplay(t *testing.T) {
	cache := newSourceTokenCache(4, 1<<20)
	source := []byte("def f() -> i64:\n    return 21\n")
	for i := 0; i < 2; i++ {
		file, ok := parseProgramWithTokenCache("snapshot.elisa", source, io.Discard, cache)
		if !ok {
			t.Fatal("parse failed")
		}
		if i == 0 {
			for _, entry := range cache.entries {
				if entry.parsed != nil {
					t.Fatal("one-off revision captured")
				}
			}
		}
		file.Decls[0].(*ast.FuncDecl).Body = nil
	}
	if cache.parseHits != 0 {
		t.Fatal("unexpected early replay")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, ok := parseProgramWithTokenCache("snapshot.elisa", source, io.Discard, cache)
			if !ok {
				t.Error("replay failed")
				return
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			if len(fn.Body) != 1 {
				t.Error("semantic/caller mutation leaked into pristine cache")
			}
			fn.Body = nil
		}()
	}
	wg.Wait()
	if cache.parseHits != 8 {
		t.Fatalf("no replay: %d", cache.parseHits)
	}
	total := 0
	for _, entry := range cache.entries {
		total += entry.bytes
	}
	if total != cache.bytes || total > cache.maxBytes {
		t.Fatal("snapshot accounting escaped shared budget")
	}
	edited := bytes.ReplaceAll(source, []byte("21"), []byte("42"))
	file, ok := parseProgramWithTokenCache("snapshot.elisa", edited, io.Discard, cache)
	if !ok || file.Decls[0].(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value.(*ast.IntLit).Value != "42" {
		t.Fatal("edit reused stale syntax")
	}
}

func TestCompileServerSyntaxReplayAfterDefaultArgumentMutation(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Filename: "defaults_snapshot.elisa", Source: "def add(x: i64, y: i64 = 7) -> i64:\n    return x + y\n\ndef main() -> i64:\n    return add(14)\n", Incremental: true}
	for _, mode := range []string{"semantic", "lowered", "llvm", "ir", "semantic", "lowered"} {
		request.Mode = mode
		actual, status := executeCompileServerRequestWithSessions(request, cache)
		expected, wantStatus := executeCompileServerRequest(request)
		if !actual.OK || status != wantStatus || !compileServerResponsesEqual(actual, expected) {
			t.Fatalf("default argument replay changed %s: %#v / %#v", mode, actual, expected)
		}
	}
	if cache.tokens.parseHits < 4 {
		t.Fatal("fixture never replayed syntax")
	}
}

func TestSourceSyntaxCacheReplaysActualEditedPrefixes(t *testing.T) {
	const count = 64
	var source strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	original := source.String()
	cache := newSourceTokenCache(2, 8<<20)
	for i := 0; i < 2; i++ {
		if _, ok := parseProgramWithTokenCache("prefix.elisa", []byte(original), io.Discard, cache); !ok {
			t.Fatal("prime failed")
		}
	}
	for edit := 0; edit < 8; edit++ {
		index := strings.LastIndex(original, "+ 1")
		changed := original[:index] + strings.Replace(original[index:], "+ 1", fmt.Sprintf("+ %d + 3", edit+2), 1)
		var warmErrors, coldErrors bytes.Buffer
		got, ok := parseProgramWithTokenCache("prefix.elisa", []byte(changed), &warmErrors, cache)
		want, wantOK := parseProgram("prefix.elisa", []byte(changed), &coldErrors)
		if ok != wantOK || warmErrors.String() != coldErrors.String() || !reflect.DeepEqual(got, want) {
			t.Fatalf("edit %d stale syntax", edit)
		}
	}
	if cache.prefixParsedDeclarations < 8*(count-1) {
		t.Fatalf("no actual edited-declaration replay: %d", cache.prefixParsedDeclarations)
	}
	total := 0
	for _, entry := range cache.entries {
		total += entry.bytes
	}
	if len(cache.entries) > 2 || total != cache.bytes || total > cache.maxBytes {
		t.Fatal("snapshot/trace escaped shared budget")
	}
}

func BenchmarkSourceSyntaxCacheUniqueTailEdits(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 2048; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	original := source.String()
	index := strings.LastIndex(original, "+ 1")
	for _, mode := range []string{"tokens", "declarations"} {
		b.Run(mode, func(b *testing.B) {
			cache := newSourceTokenCache(2, defaultSourceTokenCacheBytes)
			for prime := 0; prime < 2; prime++ {
				if _, ok := parseProgramWithTokenCache("edits.elisa", []byte(original), io.Discard, cache); !ok {
					b.Fatal("prime")
				}
				if mode == "tokens" {
					for key, entry := range cache.entries {
						entry.parseDeclined = true
						cache.entries[key] = entry
					}
				}
			}
			sources := make([][]byte, b.N)
			for i := range sources {
				sources[i] = []byte(original[:index] + strings.Replace(original[index:], "+ 1", fmt.Sprintf("+ %d + 3", i+2), 1))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for _, changed := range sources {
				if _, ok := parseProgramWithTokenCache("edits.elisa", changed, io.Discard, cache); !ok {
					b.Fatal("parse")
				}
			}
			b.StopTimer()
			if mode == "declarations" && cache.prefixParsedDeclarations < uint64(b.N*2047) {
				b.Fatal("no declaration reuse")
			}
		})
	}
}

func TestCompileServerEditedDeclarationReplayAfterSemanticMutation(t *testing.T) {
	source := "def first(x: i64 = 7) -> i64:\n    return x\n\ndef second() -> i64:\n    return first()\n\ndef main() -> i64:\n    return second() + 1\n"
	cache := newCompileServerSessionCache(2)
	request := compileServerRequest{Filename: "edited_modes.elisa", Source: source, Incremental: true}
	for _, mode := range []string{"semantic", "lowered"} {
		request.Mode = mode
		actual, _ := executeCompileServerRequestWithSessions(request, cache)
		if !actual.OK {
			t.Fatal(actual)
		}
	}
	for i, mode := range []string{"semantic", "lowered", "llvm", "ir"} {
		request.Mode = mode
		request.Source = strings.Replace(source, "second() + 1", fmt.Sprintf("second() + %d + 3", i+2), 1)
		actual, status := executeCompileServerRequestWithSessions(request, cache)
		expected, wantStatus := executeCompileServerRequest(request)
		if !actual.OK || status != wantStatus || !compileServerResponsesEqual(actual, expected) {
			t.Fatalf("edited replay after semantic mutation: %s: %#v / %#v", mode, actual, expected)
		}
	}
	if cache.tokens.prefixParsedDeclarations < 8 {
		t.Fatalf("no actual declaration replay: %d", cache.tokens.prefixParsedDeclarations)
	}
}

func TestSourceSyntaxCacheChainedSnapshotsAndEviction(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	original := source.String()
	index := strings.LastIndex(original, "+ 1")
	for _, capacity := range []int{1, 2} {
		cache := newSourceTokenCache(capacity, 2<<20)
		for revision := 0; revision < 6; revision++ {
			changed := original[:index] + strings.Replace(original[index:], "+ 1", fmt.Sprintf("+ %d + 3", revision+2), 1)
			for request := 0; request < 3; request++ {
				var warmErrors, coldErrors bytes.Buffer
				got, ok := parseProgramWithTokenCache("chain.elisa", []byte(changed), &warmErrors, cache)
				want, wantOK := parseProgram("chain.elisa", []byte(changed), &coldErrors)
				if ok != wantOK || warmErrors.String() != coldErrors.String() || !reflect.DeepEqual(got, want) {
					t.Fatalf("chained snapshot stale at %d/%d/%d", capacity, revision, request)
				}
				// Later snapshots must not inherit semantic/caller mutation of this tree.
				got.Decls[0].(*ast.FuncDecl).Body = nil
			}
			total := 0
			for _, entry := range cache.entries {
				total += entry.bytes
			}
			if len(cache.entries) > capacity || cache.bytes != total || total > cache.maxBytes {
				t.Fatal("chained snapshot budget escaped")
			}
		}
		if cache.prefixParsedDeclarations < 5*31 {
			t.Fatalf("no replay across snapshot generations/eviction: %d", cache.prefixParsedDeclarations)
		}
	}
}

func TestSourceSyntaxCacheConcurrentAdmissionKeepsTrace(t *testing.T) {
	cache := newSourceTokenCache(2, 2<<20)
	source := []byte("def first() -> i64:\n    return 1\n\ndef last() -> i64:\n    return 2\n")
	tokens, _ := cache.lex("admission.elisa", source)
	// A cold request started before another request announced the repeat.
	plain := parser.New(append([]lexer.Token(nil), tokens...))
	first := plain.ParseFile("admission.elisa")
	if _, hit, trace := cache.parsedFile("admission.elisa", source); hit || !trace {
		t.Fatal("repeat not eligible for trace")
	}
	traced := parser.NewWithSyntaxTrace(append([]lexer.Token(nil), tokens...))
	second := traced.ParseFile("admission.elisa")
	// Publish A before B, deterministically reproducing the adverse race order.
	cache.rememberParsed("admission.elisa", source, first, nil, true)
	for _, entry := range cache.entries {
		if entry.parsed != nil {
			t.Fatal("untraced request stole snapshot admission")
		}
	}
	cache.rememberParsed("admission.elisa", source, second, traced.SyntaxTrace(), true)
	candidate := cache.prefixCandidate("admission.elisa")
	if candidate.parsed == nil || candidate.trace == nil {
		t.Fatal("traced admission lost")
	}
	changed := bytes.ReplaceAll(source, []byte("return 2"), []byte("return 3 + 4"))
	got, ok := parseProgramWithTokenCache("admission.elisa", changed, io.Discard, cache)
	want, wantOK := parseProgram("admission.elisa", changed, io.Discard)
	if !ok || !wantOK || !reflect.DeepEqual(got, want) || cache.prefixParsedDeclarations < 1 {
		t.Fatal("concurrent publication prevented subsequent edited-prefix replay")
	}
}

func TestRequestTokensConcurrentParsesKeepIndependentASTs(t *testing.T) {
	cache := newRequestSourceTokenCache()
	source := []byte("def value() -> i64:\n    return 21\n")
	const count = 16
	files := make([]*ast.File, count)
	var wg sync.WaitGroup
	for i := range files {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var output bytes.Buffer
			file, ok := parseProgramWithTokenCache("request.elisa", source, &output, cache)
			if !ok || output.Len() != 0 {
				t.Errorf("parse failed: %s", output.String())
				return
			}
			files[i] = file
		}(i)
	}
	wg.Wait()
	if cache.requestLexed == nil || len(cache.entries) != 0 {
		t.Fatal("request captured persistent/syntax entries")
	}
	for i, file := range files {
		if file == nil || !reflect.DeepEqual(file, files[0]) {
			t.Fatal("concurrent parse changed syntax")
		}
		for j := 0; j < i; j++ {
			if file == files[j] || file.Decls[0] == files[j].Decls[0] {
				t.Fatal("mutable AST identity shared")
			}
		}
	}
	first, _ := cache.lexBorrowedWithKey(sourceTokenKey{"request.elisa", cache.requestKey.source}, source)
	second, _ := cache.lexBorrowedWithKey(sourceTokenKey{"request.elisa", cache.requestKey.source}, source)
	if &first[0] != &second[0] {
		t.Fatal("immutable token product was duplicated")
	}
}

func TestRequestTokensPreserveRecoveryDiagnosticsAndSourceAuthority(t *testing.T) {
	for _, source := range []string{
		"def f():\n    to: i64 = 3\n",
		"def f():\n    return \"unterminated\n",
		"def f() -> i64:\n    return 1\n",
	} {
		cache := newRequestSourceTokenCache()
		for _, filename := range []string{"request.elisa", "other.elisa"} {
			for _, text := range []string{source, source + "# edited\n", source} {
				var cold, shared bytes.Buffer
				want, wantOK := parseProgram(filename, []byte(text), &cold)
				got, gotOK := parseProgramWithTokenCache(filename, []byte(text), &shared, cache)
				if wantOK != gotOK || cold.String() != shared.String() || !reflect.DeepEqual(want, got) {
					t.Fatalf("request changed syntax/diagnostics for %s: %s / %s", filename, cold.String(), shared.String())
				}
			}
		}
	}
}

func BenchmarkRequestIndependentParses(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&source, "def value_%d() -> i64:\n    return %d\n", i, i)
	}
	data := []byte(source.String())
	for _, shared := range []bool{false, true} {
		name := "separate-lexers"
		if shared {
			name = "shared-tokens"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var cache *sourceTokenCache
				if shared {
					cache = newRequestSourceTokenCache()
				}
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, ok := parseProgramWithTokenCache("bench.elisa", data, io.Discard, cache); !ok {
							b.Error("parse failed")
						}
					}()
				}
				wg.Wait()
			}
		})
	}
}
