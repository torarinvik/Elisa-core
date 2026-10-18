package semantic

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func livenessFixture(depth int) *ast.FuncDecl {
	var expression func(int) ast.Expr
	expression = func(depth int) ast.Expr {
		if depth == 0 {
			return &ast.IntLit{Value: "1"}
		}
		return &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: expression(depth - 1), Right: expression(depth - 1)}
	}
	return &ast.FuncDecl{Name: "work", ReturnType: &ast.NamedType{Name: "i64"}, Body: []ast.Stmt{&ast.ReturnStmt{Value: expression(depth)}}}
}

func TestFunctionSourceLivenessMatchesFingerprint(t *testing.T) {
	for _, shape := range []string{"tree", "shared", "cycle", "bytes", "work", "depth"} {
		t.Run(shape, func(t *testing.T) {
			fn := livenessFixture(3)
			index, err := IndexFunctionSource(fn, FunctionIdentityContext{Origin: "/fixture.elisa", QualifiedName: "work"})
			if err != nil {
				t.Fatal(err)
			}
			switch shape {
			case "shared":
				value := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
				value.Right = value.Left
			case "cycle":
				value := &ast.ParenExpr{}
				value.Inner = value
				fn.Body[0].(*ast.ReturnStmt).Value = value
			case "bytes":
				fn.Name = strings.Repeat("x", functionSyntaxMaxBytes)
			case "work":
				fn.Body = make([]ast.Stmt, functionSyntaxMaxWork+1)
			case "depth":
				value := fn.Body[0].(*ast.ReturnStmt).Value
				for i := 0; i < functionSyntaxMaxDepth; i++ {
					value = &ast.ParenExpr{Inner: value}
				}
				fn.Body[0].(*ast.ReturnStmt).Value = value
			}
			_, nodes, fingerprintErr := functionSyntaxFingerprint(fn)
			live, livenessErr := index.liveSourceNodes()
			if fingerprintErr != nil || livenessErr != nil {
				if !errors.Is(fingerprintErr, ErrFunctionSyntaxBudget) || !errors.Is(livenessErr, ErrFunctionSyntaxBudget) {
					t.Fatalf("validation differs: fingerprint=%v liveness=%v", fingerprintErr, livenessErr)
				}
				return
			}
			if len(nodes) != len(live) {
				t.Fatalf("node count differs: fingerprint=%d liveness=%d", len(nodes), len(live))
			}
			for _, node := range nodes {
				if !live[node] {
					t.Fatalf("missing live source node: %T", node)
				}
			}
		})
	}
}

func BenchmarkFunctionSourceLiveness(b *testing.B) {
	index, err := IndexFunctionSource(livenessFixture(7), FunctionIdentityContext{Origin: "/fixture.elisa", QualifiedName: "work"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		live, err := index.liveSourceNodes()
		if err != nil || len(live) == 0 {
			b.Fatal("source liveness failed:", err)
		}
	}
}

func TestFunctionSyntaxEncodingCompatibility(t *testing.T) {
	digest, _, err := functionSyntaxFingerprint(livenessFixture(3))
	if err != nil {
		t.Fatal(err)
	}
	// Captured from the previous full-buffer encoder: optimization must not
	// silently change the identity protocol used by existing query products.
	const want = "872376fee5032ca36203b2d2366a674b0f3cfc69b31a34cad2b8d4263628d936"
	if got := fmt.Sprintf("%x", digest); got != want {
		t.Fatalf("fingerprint encoding changed: got %s, want %s", got, want)
	}
}
