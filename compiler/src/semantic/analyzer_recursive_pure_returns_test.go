package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestRecursiveTerminationCertificateDeclinesReentry(t *testing.T) {
	call := &ast.CallExpr{}
	fn := &ast.FuncDecl{}
	a := &Analyzer{recursiveCallCertificatesInFlight: map[recursiveCallCertificateKey]bool{{caller: fn, callee: fn}: true}}
	cert, ok := a.recursiveCallCertificateUnderPath(fn, fn, call, nil)
	if ok || cert.Outcome != ProofRuntime || cert.Call != call {
		t.Fatalf("re-entered termination proof must decline opaquely, got cert=%+v ok=%v", cert, ok)
	}
}

func TestPureReturnExtractionDeclinesAtStatementDepthLimit(t *testing.T) {
	stmts := make([]ast.Stmt, 0, pureReturnMaxDepth+1)
	for i := 0; i < pureReturnMaxDepth; i++ {
		stmts = append(stmts, &ast.VarDeclStmt{
			Name:  "local",
			Value: &ast.IntLit{Value: "1"},
		})
	}
	stmts = append(stmts, &ast.ReturnStmt{Value: &ast.Ident{Name: "local"}})
	work := pureReturnMaxWork
	if _, ok := pureReturnCasesFromStmts(stmts, nil, nil, &work, 0); ok {
		t.Fatal("deep pure-return extraction must decline instead of recursing without a bound")
	}
}

func TestLocalReturnEquationPreservesFixedWidthArithmetic(t *testing.T) {
	src := `
def inc(x: i64) -> i64:
    y: i64 = x + 1
    return y

def caller(x: i64) -> i64:
    ensure result > x
    return inc(x)
`
	errs := strings.Join(analyzeContractStrict(t, "local_return_overflow.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("local-return equation must respect i64 wraparound, got: %v", errs)
	}
}
