package semantic

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Reference retains the original two-scan behavior as an equivalence oracle.
func retainedSpansReference(decls []ast.Decl, src []byte) []string {
	starts := make([]int, 0, len(decls))
	for _, d := range decls {
		starts = append(starts, retainedDeclSourceStart(d))
	}
	sort.Ints(starts)
	out := make([]string, len(decls))
	for i, d := range decls {
		start, end := retainedDeclSourceStart(d), len(src)
		if k := sort.SearchInts(starts, start+1); k < len(starts) {
			end = starts[k]
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i)
			continue
		}
		span := src[start:end]
		if k := bytes.Index(span, []byte("\n#line ")); k >= 0 {
			span = span[:k+1]
		}
		out[i] = retainedWithoutLineDirectives(span)
	}
	return out
}

func TestRetainedSpansFastMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(312))
	parts := []string{"code", "#line 1 file\n", "\n#line 2 file\n", "\"#line text\"", "\n", ""}
	for trial := 0; trial < 200; trial++ {
		var text strings.Builder
		for n := 0; n < 20; n++ {
			text.WriteString(parts[rng.Intn(len(parts))])
		}
		src := []byte(text.String())
		decls := make([]ast.Decl, 30)
		for i := range decls {
			decls[i] = &ast.FuncDecl{Position: lexer.Pos{Offset: rng.Intn(len(src)+3) - 1}}
		}
		if got, want := retainedSpans(decls, src), retainedSpansReference(decls, src); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d changed declaration spans", trial)
		}
	}
}
