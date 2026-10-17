package main

import (
	"bytes"
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/semantic"
)

// Opt-in local project probe. Includes/load are outside each timed frontend run;
// their sourceFiles authority is identical on cold and incremental paths.
// No project source files are modified. This checks frontend results, not native
// compilation speed; error-bearing projects are valid diagnostic parity workloads.
type projectProbeSnapshot struct {
	Filename    string
	Source      string
	SourceFiles []string
}

func TestLocalIncrementalProjectProbe(t *testing.T) {
	path := os.Getenv("ELISA_INCREMENTAL_PROJECT_PROBE")
	snapshotIn := os.Getenv("ELISA_INCREMENTAL_PROJECT_PROBE_SNAPSHOT_IN")
	if path == "" && snapshotIn == "" {
		t.Skip("set ELISA_INCREMENTAL_PROJECT_PROBE or its SNAPSHOT_IN variant")
	}
	var loaded *loadedProgram
	started := time.Now()
	if snapshotIn != "" {
		data, err := os.ReadFile(snapshotIn)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot projectProbeSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Filename == "" || snapshot.Source == "" {
			t.Fatal("snapshot requires filename and expanded source")
		}
		path = snapshot.Filename
		loaded = &loadedProgram{filename: path, source: []byte(snapshot.Source), sourceFiles: snapshot.SourceFiles}
	} else {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		path = absolute
		var loadErrors bytes.Buffer
		var ok bool
		loaded, ok = loadProgramInput(path, &loadErrors)
		if !ok {
			t.Fatalf("load: %s", loadErrors.String())
		}
	}
	if snapshotOut := os.Getenv("ELISA_INCREMENTAL_PROJECT_PROBE_SNAPSHOT_OUT"); snapshotOut != "" {
		data, err := json.Marshal(projectProbeSnapshot{Filename: loaded.filename, Source: string(loaded.source), SourceFiles: loaded.sourceFiles})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotOut, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("load_ms=%.3f source_bytes=%d included_files=%d source_sha256=%x", float64(time.Since(started).Microseconds())/1000, len(loaded.source), len(loaded.sourceFiles), sha256.Sum256(loaded.source))
	// Diagnose whether the current parse can become a snapshot provider.
	lex := lexer.New(path, loaded.source)
	tokens := lex.Tokenize()
	parsed := parser.NewWithSyntaxTrace(tokens)
	syntax := parsed.ParseFile(path)
	var captureError error
	if len(lex.Errors()) == 0 && len(parsed.Errors()) == 0 {
		_, captureError = ast.NewSyntaxSnapshot(syntax, defaultSourceTokenCacheBytes)
	}
	t.Logf("tokens=%d lex_errors=%d parse_errors=%d parser_notices=%d trace_eligible=%v capture_error=%v", len(tokens), len(lex.Errors()), len(parsed.Errors()), len(parsed.Notices()), parsed.SyntaxTrace() != nil, captureError)
	cache := newCompileServerSessionCache(4)
	session := cache.session(path, emitSemantic)
	run := func(label string, source []byte, incremental bool) compileServerResponse {
		program := *loaded
		program.source = source
		var analysisSession *semantic.IncrementalAnalysisSession
		if incremental {
			program.tokenCache = cache.tokens
			analysisSession = session
		}
		var diagnostics bytes.Buffer
		start := time.Now()
		_, result, success := analyzeLoadedProgramWithSession(&program, &diagnostics, semantic.AnalyzeOptions{}, analysisSession)
		elapsed := time.Since(start)
		response := compileServerResponse{OK: success, Stderr: strings.TrimSpace(diagnostics.String())}
		if success {
			response.Output = generateSemanticReport(result)
		}
		record := map[string]any{"source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "label": label, "elapsed_ms": float64(elapsed.Microseconds()) / 1000, "ok": success, "diagnostic_bytes": len(response.Stderr), "diagnostic_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(response.Stderr))), "token_hits": cache.tokens.hits, "token_misses": cache.tokens.misses, "parse_hits": cache.tokens.parseHits, "prefix_declarations": cache.tokens.prefixParsedDeclarations, "retained_source_bytes": cache.tokens.bytes}
		if incremental {
			record["semantic_stats"] = session.Stats()
		}
		encoded, _ := json.Marshal(record)
		t.Log(string(encoded))
		return response
	}
	cold := run("stateless_base", loaded.source, false)
	if os.Getenv("ELISA_INCREMENTAL_PROJECT_PROBE_PROFILE_ONLY") == "1" {
		return
	}
	for _, label := range []string{"incremental_first", "incremental_repeat", "incremental_exact_replay"} {
		actual := run(label, loaded.source, true)
		if !compileServerResponsesEqual(cold, actual) {
			t.Fatalf("%s differs from stateless result", label)
		}
	}
	// A real syntax edit, independent of the project's own public names/contracts.
	changed := append(append([]byte(nil), loaded.source...), []byte("\ndef __incremental_project_probe_added() -> i64:\n    return 42\n")...)
	expected := run("stateless_added_function", changed, false)
	actual := run("incremental_added_function", changed, true)
	if !compileServerResponsesEqual(expected, actual) {
		t.Fatal("edited project differs from stateless result")
	}
}
