//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// This stresses traversal occurrence order only. Analyze retains its existing
// pointer-keyed invocation maps; this does not add support for aliased source ASTs.
func TestIncrementalOwnerCandidatesPreserveTraversalOccurrencesAndFallback(t *testing.T) {
	fn := &ast.FuncDecl{Name: "work"}
	decls := []ast.Decl{fn, &ast.NamespaceDecl{Name: "outer", Decls: []ast.Decl{fn}}, fn}
	firstIdentity, _, _ := preparePrimitiveOwnerCandidate(fn, "work", "owner-test.elisa", nil)
	previous := map[incremental.Key]bool{primitiveInput("function-source", primitiveOwnerCandidateName(firstIdentity)): true}
	for _, limit := range []int{0, 1, 3, primitiveOwnerCandidateLimit} {
		sequence := primitiveOwnerCandidates(decls, "owner-test.elisa", previous, limit, false)
		var passes [][]primitiveOwnerCandidate
		for pass := 0; pass < 2; pass++ {
			var candidates []primitiveOwnerCandidate
			for candidate := range sequence {
				candidates = append(candidates, candidate)
			}
			passes = append(passes, candidates)
		}
		if !reflect.DeepEqual(passes[0], passes[1]) || len(passes[0]) != 3 {
			t.Fatal("pass order/fallback changed", limit, passes)
		}
		candidates := passes[0]
		if candidates[0].identity != candidates[2].identity || candidates[0].identity == candidates[1].identity || !candidates[0].retained || candidates[1].retained || !candidates[2].retained {
			t.Fatal("namespace or duplicate occurrence identity lost", limit, candidates)
		}
	}
}

func TestIncrementalOwnerCandidatesFallBackBeforeAggregateWorkBudget(t *testing.T) {
	const nameBytes = 600 * 1024
	decls := make([]ast.Decl, 9)
	previous := make(map[incremental.Key]bool, len(decls))
	for i := range decls {
		fn := &ast.FuncDecl{Name: fmt.Sprintf("%d%s", i, strings.Repeat("x", nameBytes-1))}
		decls[i] = fn
		identity, _, _ := preparePrimitiveOwnerCandidate(fn, fn.Name, "owner-test.elisa", nil)
		previous[primitiveInput("function-source", primitiveOwnerCandidateName(identity))] = true
	}
	sequence := primitiveOwnerCandidates(decls, "owner-test.elisa", previous, primitiveOwnerCandidateLimit, false)
	for pass := 0; pass < 2; pass++ {
		count := 0
		for candidate := range sequence {
			if !candidate.retained {
				t.Fatalf("candidate %d lost retained identity after fallback", count)
			}
			count++
		}
		if count != len(decls) {
			t.Fatalf("pass %d visited %d owners, want %d", pass, count, len(decls))
		}
	}
}

func TestReservePrimitiveOwnerCandidateBytesIsOverflowSafe(t *testing.T) {
	used := primitiveOwnerCandidateWorkBudget - 1
	if !reservePrimitiveOwnerCandidateBytes(&used, 1, primitiveOwnerCandidateWorkBudget) || used != primitiveOwnerCandidateWorkBudget {
		t.Fatal("reservation exactly reaching the budget should succeed")
	}
	if reservePrimitiveOwnerCandidateBytes(&used, 1, primitiveOwnerCandidateWorkBudget) || used != primitiveOwnerCandidateWorkBudget {
		t.Fatal("reservation beyond the budget should be rejected")
	}
	used = 0
	if reservePrimitiveOwnerCandidateBytes(&used, int(^uint(0)>>1), primitiveOwnerCandidateWorkBudget) {
		t.Fatal("oversized reservation should be rejected without overflow")
	}
}

var benchmarkOwnerCandidateSink int

// This isolates the actual production candidate preparation plus both admission
// iterations. Streaming is the prior repeated identity/classification work;
// buffered mode includes building and discarding its invocation-local slice.
func BenchmarkPrimitiveOwnerCandidatePasses(b *testing.B) {
	for _, count := range []int{16, 512, 4096, primitiveOwnerCandidateLimit - 1, primitiveOwnerCandidateLimit + 1} {
		decls := make([]ast.Decl, count)
		previous := make(map[incremental.Key]bool, count)
		for i := range decls {
			fn := &ast.FuncDecl{Name: fmt.Sprintf("work%d", i)}
			decls[i] = fn
			identity, _, _ := preparePrimitiveOwnerCandidate(fn, fn.Name, "candidate-benchmark.elisa", nil)
			previous[primitiveInput("function-source", primitiveOwnerCandidateName(identity))] = true
		}
		for _, mode := range []struct {
			name  string
			limit int
		}{{"streaming", 0}, {"buffered", primitiveOwnerCandidateLimit}} {
			b.Run(fmt.Sprintf("owners=%d/%s", count, mode.name), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					candidates := primitiveOwnerCandidates(decls, "candidate-benchmark.elisa", previous, mode.limit, false)
					total := 0
					for pass := 0; pass < 2; pass++ {
						for candidate := range candidates {
							if candidate.retained != (pass == 0) {
								continue
							}
							name := candidate.name
							if name == "" {
								name = primitiveOwnerCandidateName(candidate.identity)
							}
							key := primitiveInput("function-source", name)
							total += len(key.Name)
						}
					}
					if total != count*(len("primitive-v1:")+64) {
						b.Fatal("owner iteration lost records", total)
					}
					benchmarkOwnerCandidateSink = total
				}
			})
		}
	}
}

// Includes the production Analyze path and retained admission, with fresh AST
// parsing outside timing. Every iteration edits exactly one of 512 owners.
func BenchmarkIncrementalOwnerDiscoveryEdit(b *testing.B) {
	const count = 512
	var source strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return value + 1\n\n", i)
	}
	base := source.String()
	edited := strings.Replace(base, "return value + 1", "return value + 2", 1)
	parse := func(source string) *ast.File {
		l := lexer.New("owner-edit-benchmark.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("owner-edit-benchmark.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid owner-edit fixture")
		}
		return file
	}
	session := NewIncrementalAnalysisSession()
	if result := session.Analyze(parse(base), AnalyzeOptions{}); len(result.Errors()) != 0 {
		b.Fatal(result.Errors())
	}
	b.ReportAllocs()
	b.ResetTimer()
	var reused, checked uint64
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		current := edited
		if i%2 != 0 {
			current = base
		}
		file := parse(current)
		before := session.Stats()
		b.StartTimer()
		result := session.Analyze(file, AnalyzeOptions{})
		b.StopTimer()
		if len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		after := session.Stats()
		reused += after.PrimaryBodiesReused - before.PrimaryBodiesReused
		checked += after.PrimaryBodiesChecked - before.PrimaryBodiesChecked
	}
	if reused != uint64(b.N*(count-1)) || checked != uint64(b.N) {
		b.Fatal("unexpected edit locality", reused, checked, b.N)
	}
	b.ReportMetric(float64(reused)/float64(b.N), "body-reuses/edit")
	b.ReportMetric(float64(checked)/float64(b.N), "body-checks/edit")
}
