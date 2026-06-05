package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeAndGetFile(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("auto_reserve.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("auto_reserve.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	AnalyzeWithOptions(file, AnalyzeOptions{})
	return file
}

func firstIterForStmt(root any) *ast.IterForStmt {
	var found *ast.IterForStmt
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found != nil || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if loop, ok := v.Interface().(*ast.IterForStmt); ok {
				found = loop
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(root))
	return found
}

// A `for x in src:` loop over a darray that fills exactly one darray gets an auto-inserted
// `ys.reserve(src.count)` (recorded on the loop's PreReserve slot).
func TestAutoReserveForInSetsPreReserve(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    for x in src:
        ys.push(x)
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on the for-in fill loop")
	}
}

// Two darrays filled in one loop is ambiguous (which to presize?) — skipped.
func TestAutoReserveForInSkipsAmbiguousFill(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    zs: mutable darray[i64] = []
    for x in src:
        ys.push(x)
        zs.push(x)
    return ys[0] + zs[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve != nil {
		t.Fatal("ambiguous multi-target fill must not auto-reserve")
	}
}
