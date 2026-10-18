//go:build cgo

package semantic

import (
	"fmt"
	"runtime"
	"testing"
	"weak"

	"elisacore/src/ast"
)

func analyzedInvocationWeakPointers(t *testing.T, session *IncrementalAnalysisSession, revision int) (weak.Pointer[Analyzer], weak.Pointer[ast.File]) {
	t.Helper()
	source := fmt.Sprintf("def id[T](x: T) -> T:\n    return x\n\ndef stable(value: i64) -> i64:\n    return value + 1\n\ndef edited() -> i64:\n    return %d\n", revision)
	file := parseIncrementalSessionFile(t, source)
	result := session.Analyze(file, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "id" {
			for i := 0; i < 16; i++ {
				if overlay := result.SpecializedExprTypes(fn, []Type{result.analyzer.namedTypes["i64"]}); len(overlay) == 0 {
					t.Fatal("fixture did not produce specialized types")
				}
			}
		}
	}
	return weak.Make(result.analyzer), weak.Make(result.File)
}

func TestIncrementalSessionReleasesCompletedAnalyzers(t *testing.T) {
	for _, audit := range []bool{false, true} {
		t.Run(fmt.Sprintf("audit_%v", audit), func(t *testing.T) {
			session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: audit})
			var analyzers []weak.Pointer[Analyzer]
			var files []weak.Pointer[ast.File]
			for revision := 0; revision < 3; revision++ {
				analyzer, file := analyzedInvocationWeakPointers(t, session, revision)
				analyzers = append(analyzers, analyzer)
				files = append(files, file)
			}
			if session.Stats().PrimaryBodiesReused == 0 {
				t.Fatal("fixture did not exercise retained typing products")
			}
			collected := func() bool {
				for _, value := range analyzers {
					if value.Value() != nil {
						return false
					}
				}
				for _, value := range files {
					if value.Value() != nil {
						return false
					}
				}
				return true
			}
			released := false
			for attempt := 0; attempt < 8; attempt++ {
				runtime.GC()
				if collected() {
					released = true
					break
				}
			}
			runtime.KeepAlive(session)
			if !released {
				t.Fatal("retained session kept completed analyzer/source AST reachable")
			}
		})
	}
}
