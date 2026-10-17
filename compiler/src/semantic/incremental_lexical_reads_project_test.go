//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLocalLexicalReadPointProjectProbe(t *testing.T) {
	path := os.Getenv("ELISA_LOOKUP_QUERY_PROBE_SNAPSHOT")
	if path == "" {
		t.Skip("set frozen project snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ Filename, Source string }
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Filename == "" || snapshot.Source == "" {
		t.Fatal("empty frozen project")
	}
	parse := func() *ast.File {
		lex := lexer.New(snapshot.Filename, []byte(snapshot.Source))
		tokens := lex.Tokenize()
		p := parser.New(tokens)
		file := p.ParseFile(snapshot.Filename)
		if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
			t.Fatal("frozen syntax changed")
		}
		return file
	}
	session := NewIncrementalAnalysisSession()
	for pass := 0; pass < 2; pass++ {
		file := parse()
		before := session.Stats()
		start := time.Now()
		result := session.Analyze(file, AnalyzeOptions{})
		elapsed := time.Since(start)
		after := session.Stats()
		if after.LexicalSelectionsVerified <= before.LexicalSelectionsVerified || after.LexicalVerificationFailures != 0 || after.LexicalPublicationFailures != 0 {
			t.Fatalf("project read-point publication failed: %+v", after)
		}
		if pass == 1 && after.LexicalQueryMemoHits <= before.LexicalQueryMemoHits {
			t.Fatal("no project memo reuse")
		}
		t.Logf("captures=%d frames=%d", after.LexicalCapturedObservations-before.LexicalCapturedObservations, after.LexicalCapturedFrames-before.LexicalCapturedFrames)
		t.Logf("binder observed=%d verified=%d dropped=%d", after.LexicalBinderReadsObserved-before.LexicalBinderReadsObserved, after.LexicalBinderSelectionsVerified-before.LexicalBinderSelectionsVerified, after.LexicalBinderReadsDropped-before.LexicalBinderReadsDropped)
		t.Logf("pass%d duration=%s errors=%d indexes=%d observed=%d verified=%d dropped=%d executed=%d hits=%d skipped=%d owner=%+v", pass, elapsed, len(result.Errors()), after.LexicalSourceIndexesBuilt-before.LexicalSourceIndexesBuilt, after.LexicalReadsObserved-before.LexicalReadsObserved, after.LexicalSelectionsVerified-before.LexicalSelectionsVerified, after.LexicalReadsDropped-before.LexicalReadsDropped, after.LexicalQueriesExecuted-before.LexicalQueriesExecuted, after.LexicalQueryMemoHits-before.LexicalQueryMemoHits, after.LexicalQueriesSkipped-before.LexicalQueriesSkipped, session.lexical.Stats())
	}
}
