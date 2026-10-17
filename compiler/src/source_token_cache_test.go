package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSourceTokenCacheOwnsSourceAndParserTokens(t *testing.T) {
	cache := newSourceTokenCache(4, 1<<20)
	original := "def value() -> i64:\n    return 21\n"
	input := []byte(original)
	tokens, errs := cache.lex("owned.elisa", input)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	expected := lexer.New("owned.elisa", []byte(original)).Tokenize()
	for i := range input {
		input[i] = 'x'
	}
	tokens[0].Kind = lexer.TOKEN_EOF
	tokens[0].Text = "changed"
	got, errs := cache.lex("owned.elisa", []byte(original))
	if len(errs) != 0 || !reflect.DeepEqual(got, expected) {
		t.Fatalf("cache source/token alias corrupted entry: %#v", got)
	}
	var firstErrors, secondErrors bytes.Buffer
	first, ok := parseProgramWithTokenCache("owned.elisa", []byte(original), &firstErrors, cache)
	if !ok {
		t.Fatal(firstErrors.String())
	}
	second, ok := parseProgramWithTokenCache("owned.elisa", []byte(original), &secondErrors, cache)
	if !ok || !reflect.DeepEqual(first, second) {
		t.Fatal("parse parity failed")
	}
	if first == second || first.Decls[0] == second.Decls[0] {
		t.Fatal("AST reused between parses")
	}
}

func TestSourceTokenCachePreservesDiagnosticsAndFilename(t *testing.T) {
	cache := newSourceTokenCache(8, 1<<20)
	for _, source := range []string{
		"def f():\n    to: i64 = 3\n",
		"def f():\n    return \"unterminated\n",
		"def f() -> i64:\n    return 1\n",
	} {
		for _, filename := range []string{"first.elisa", "other.elisa"} {
			var cold bytes.Buffer
			expected, expectedOK := parseProgram(filename, []byte(source), &cold)
			for repeat := 0; repeat < 3; repeat++ {
				var warm bytes.Buffer
				got, ok := parseProgramWithTokenCache(filename, []byte(source), &warm, cache)
				if ok != expectedOK || warm.String() != cold.String() || !reflect.DeepEqual(got, expected) {
					t.Fatalf("diagnostics/parse changed: %q cold %v/%s warm %v/%s", source, expectedOK, cold.String(), ok, warm.String())
				}
			}
		}
	}
	if cache.hits == 0 {
		t.Fatal("no token reuse")
	}
}

func TestSourceTokenCacheLRUAndBytes(t *testing.T) {
	cache := newSourceTokenCache(2, 1<<20)
	source := []byte("def f():\n    pass\n")
	cache.lex("a.elisa", source)
	cache.lex("b.elisa", source)
	cache.lex("a.elisa", source)
	cache.lex("c.elisa", source)
	if len(cache.entries) != 2 {
		t.Fatal("entry bound exceeded")
	}
	before := cache.misses
	cache.lex("b.elisa", source)
	if cache.misses != before+1 {
		t.Fatal("least-recently-used entry retained")
	}
	tiny := newSourceTokenCache(4, 128)
	tiny.lex("a.elisa", source)
	if len(tiny.entries) != 0 || tiny.bytes != 0 {
		t.Fatal("oversized token entry admitted")
	}
	comments := []byte(strings.Repeat("# padding\n", 4096) + "const x = 1\n")
	bounded := newSourceTokenCache(4, 4096)
	bounded.lex("padding.elisa", comments)
	if len(bounded.entries) != 0 || bounded.bytes != 0 {
		t.Fatal("comment backing storage escaped byte budget")
	}
}

func TestSourceTokenCacheConcurrentPrivateSlices(t *testing.T) {
	cache := newSourceTokenCache(4, 1<<20)
	source := []byte("def f() -> i64:\n    return 1\n")
	cache.lex("same.elisa", source)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repeat := 0; repeat < 4; repeat++ {
				tokens, errs := cache.lex("same.elisa", source)
				if len(errs) != 0 {
					t.Error(errs)
				}
				tokens[0].Kind = lexer.TOKEN_EOF
			}
		}()
	}
	wg.Wait()
	tokens, _ := cache.lex("same.elisa", source)
	if tokens[0].Kind != lexer.TOKEN_DEF {
		t.Fatal("shared mutable token slice")
	}
}

func TestCompileServerTokenReuseIsOptInAndInvalidatesEdits(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	req := compileServerRequest{Mode: "lowered", Filename: "tokens.elisa", Source: "def f() -> i64:\n    return 21\n"}
	cold, status := executeCompileServerRequestWithSessions(req, cache)
	if status != http.StatusOK || !cold.OK {
		t.Fatal(cold)
	}
	if cache.tokens.hits != 0 || cache.tokens.misses != 0 {
		t.Fatal("stateless request retained tokens")
	}
	req.Incremental = true
	for _, source := range []string{req.Source, req.Source, strings.Replace(req.Source, "21", "42", 1), req.Source} {
		req.Source = source
		actual, status := executeCompileServerRequestWithSessions(req, cache)
		reference, expectedStatus := executeCompileServerRequest(req)
		if status != expectedStatus || !compileServerResponsesEqual(actual, reference) {
			t.Fatalf("server parity failed: %#v / %#v", actual, reference)
		}
	}
	if cache.tokens.hits < 2 || cache.tokens.misses != 2 {
		t.Fatalf("unexpected reuse %d/%d", cache.tokens.hits, cache.tokens.misses)
	}
}

func BenchmarkSourceTokenCacheParse(b *testing.B) {
	for _, count := range []int{16, 256, 2048} {
		var program strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&program, "def work%d(x: i64) -> i64:\n    return x * 3 + 1\n\n", i)
		}
		source := []byte(program.String())
		for _, mode := range []string{"cold", "tokens", "warm"} {
			b.Run(fmt.Sprintf("functions%d/%s", count, mode), func(b *testing.B) {
				var cache *sourceTokenCache
				if mode != "cold" {
					cache = newSourceTokenCache(4, defaultSourceTokenCacheBytes)
					for prime := 0; prime < 2; prime++ {
						if _, ok := parseProgramWithTokenCache("bench.elisa", source, io.Discard, cache); !ok {
							b.Fatal("prime failed")
						}
						if mode == "tokens" {
							for key, entry := range cache.entries {
								entry.parseDeclined = true
								cache.entries[key] = entry
							}
						}
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, ok := parseProgramWithTokenCache("bench.elisa", source, io.Discard, cache); !ok {
						b.Fatal("parse failed")
					}
				}
			})
		}
	}
}

func TestCompileServerTokenCacheBypassesDecodedIR(t *testing.T) {
	source := "def value() -> i64:\n    return 21\n"
	cache := newCompileServerSessionCache(4)
	_, status := executeCompileServerRequestWithSessions(compileServerRequest{Mode: "lowered", Source: source, Filename: "ir.elisa", Incremental: true}, cache)
	if status != http.StatusOK {
		t.Fatal("prime failed")
	}
	bundle, status := executeCompileServerRequest(compileServerRequest{Mode: "ir", Source: source, Filename: "ir.elisa"})
	if status != http.StatusOK || !bundle.OK {
		t.Fatal(bundle)
	}
	hits, misses := cache.tokens.hits, cache.tokens.misses
	request := compileServerRequest{Mode: "lowered", IR: bundle.IR, Filename: "ir.elisa", Incremental: true}
	actual, status := executeCompileServerRequestWithSessions(request, cache)
	expected, expectedStatus := executeCompileServerRequest(request)
	if status != expectedStatus || !compileServerResponsesEqual(actual, expected) {
		t.Fatalf("IR response changed: %#v / %#v", actual, expected)
	}
	if cache.tokens.hits != hits || cache.tokens.misses != misses {
		t.Fatal("decoded IR identified by source tokens")
	}
}

func TestCompileServerSyntaxCacheSharesPristineSyntaxAcrossModes(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Filename: "shared_modes.elisa", Source: "def value(x: i64) -> i64:\n    return x * 3 + 1\n", Incremental: true}
	for _, mode := range []string{"semantic", "llvm", "ir", "semantic", "lowered"} {
		request.Mode = mode
		actual, status := executeCompileServerRequestWithSessions(request, cache)
		expected, expectedStatus := executeCompileServerRequest(request)
		if status != expectedStatus || !compileServerResponsesEqual(actual, expected) {
			t.Fatalf("mode %s parity failed: %#v / %#v", mode, actual, expected)
		}
	}
	if cache.tokens.misses != 1 || cache.tokens.hits != 4 {
		t.Fatalf("mode reuse %d/%d", cache.tokens.hits, cache.tokens.misses)
	}
}

func TestCompileServerTokenCacheReusesEditedPrefix(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x + 1\n\n", i)
	}
	original := source.String()
	index := strings.LastIndex(original, "+ 1")
	changed := original[:index] + strings.Replace(original[index:], "+ 1", "+ 2", 1)
	cache := newCompileServerSessionCache(1)
	for step, text := range []string{original, changed, original, changed} {
		request := compileServerRequest{Mode: []string{"lowered", "semantic", "llvm", "ir"}[step], Filename: "editing.elisa", Source: text, Incremental: true}
		actual, status := executeCompileServerRequestWithSessions(request, cache)
		expected, expectedStatus := executeCompileServerRequest(request)
		if status != http.StatusOK || !actual.OK || status != expectedStatus || !compileServerResponsesEqual(actual, expected) {
			t.Fatal("edited prefix changed server output")
		}
	}
	if cache.tokens.prefixReusedTokens < 1000 {
		t.Fatalf("edited request did not skip a substantial prefix: %d", cache.tokens.prefixReusedTokens)
	}
	if len(cache.tokens.entries) != 1 || cache.tokens.bytes > cache.tokens.maxBytes {
		t.Fatal("edit history escaped cache bounds")
	}
}

func BenchmarkSourceTokenCacheEditedParse(b *testing.B) {
	for _, count := range []int{256, 2048} {
		var program strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&program, "def work%d(x: i64) -> i64:\n    return x * 3 + 1\n\n", i)
		}
		original := program.String()
		index := strings.LastIndex(original, "+ 1")
		changed := original[:index] + strings.Replace(original[index:], "+ 1", "+ 2", 1)
		sources := [][]byte{[]byte(original), []byte(changed)}
		for _, mode := range []string{"cold", "edit"} {
			b.Run(fmt.Sprintf("functions%d/%s", count, mode), func(b *testing.B) {
				var cache *sourceTokenCache
				if mode == "edit" {
					cache = newSourceTokenCache(1, defaultSourceTokenCacheBytes)
					if _, ok := parseProgramWithTokenCache("bench.elisa", sources[0], io.Discard, cache); !ok {
						b.Fatal("prime failed")
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, ok := parseProgramWithTokenCache("bench.elisa", sources[(i+1)%2], io.Discard, cache); !ok {
						b.Fatal("parse failed")
					}
				}
				if cache != nil && cache.prefixReusedTokens == 0 {
					b.Fatal("no edited-prefix reuse")
				}
			})
		}
	}
}

func TestSourceTokenCacheConcurrentEditedRevisions(t *testing.T) {
	var prefix strings.Builder
	prefix.WriteString("#line 50 shared.elisa\n")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&prefix, "def work%d():\n    if true:\n        return 1\n", i)
	}
	var sources [][]byte
	for i := 0; i < 3; i++ {
		sources = append(sources, []byte(prefix.String()+fmt.Sprintf("#line %d tail%d.elisa\ndef tail():\n    if true:\n%sreturn %d\n", 90+i, i, strings.Repeat(" ", 8+4*i), i)))
	}
	cache := newSourceTokenCache(2, defaultSourceTokenCacheBytes)
	cache.lex("concurrent.elisa", sources[0])
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for edit := 0; edit < 4; edit++ {
				source := sources[(index+edit)%len(sources)]
				actual, errors := cache.lex("concurrent.elisa", source)
				cold := lexer.New("concurrent.elisa", source)
				expected := cold.Tokenize()
				if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(errors, cold.Errors()) {
					t.Error("concurrent edit changed tokens")
				}
				actual[0].Kind = lexer.TOKEN_EOF
			}
		}(i)
	}
	wg.Wait()
	if cache.prefixReusedTokens == 0 || cache.bytes > cache.maxBytes || len(cache.entries) > 2 {
		t.Fatal("concurrent revision cache bounds/reuse failed")
	}
}

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
