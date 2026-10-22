package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestScopedFreshShapesKeepFullIdentity(t *testing.T) {
	left := &FreshShape{Scope: "first", ID: 1, Label: "row"}
	right := &FreshShape{Scope: "second", ID: 1, Label: "row"}
	if SameShape(left, right) || left.String() == right.String() {
		t.Fatal("independent owners were equated")
	}
	elem := &BuiltinType{Name: "u8"}
	array := func(s Shape) Type { return &DArrayType{Elem: elem, Shape: s} }
	pair := &FuncType{Params: []Type{array(left)}, Return: array(right)}
	if len(collectFreshShapesInType(pair)) != 2 {
		t.Fatal("fresh-shape collection collapsed different owners")
	}
	if CanonicalTypeID(array(left)) == CanonicalTypeID(array(right)) {
		t.Fatal("canonical types collapsed different owners")
	}
	distinct, err := TypeSymbolKeys([]Type{array(left), array(right)})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := TypeSymbolKeys([]Type{array(left), array(left)})
	if err != nil || distinct == repeated {
		t.Fatal("emitted symbol key lost fresh-shape alias partition")
	}
	renamed, err := TypeSymbolKeys([]Type{array(&FreshShape{Scope: "other-a", ID: 42}), array(&FreshShape{Scope: "other-b", ID: 42})})
	if err != nil || renamed != distinct {
		t.Fatal("emitted symbol key depends on opaque scope names")
	}
}

func TestFreshShapeScopesSeparateConcreteInstantiations(t *testing.T) {
	a := &Analyzer{}
	fn := &ast.FuncDecl{Name: "generic"}
	mint := func(arg Type) *FreshShape {
		restore := a.enterFreshShapeScope(fn, []Type{arg}, "body")
		defer restore()
		bindings := map[string]Shape{}
		a.bindFreshShape(&ShapeParam{Name: "row"}, "produce", bindings)
		return bindings["row"].(*FreshShape)
	}
	first := mint(&BuiltinType{Name: "i64"})
	second := mint(&BuiltinType{Name: "u64"})
	again := mint(&BuiltinType{Name: "i64"})
	constArg := func(value int64) Type {
		return &ConstValueType{Value: ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{"value": {Kind: ConstInt, Int: value}}}}
	}
	if SameShape(mint(constArg(1)), mint(constArg(2))) {
		t.Fatal("constant-record specializations share identity")
	}
	if SameShape(first, second) {
		t.Fatal("concrete reanalyses share identity")
	}
	if !SameShape(first, again) || first.String() != again.String() {
		t.Fatal("same body instantiation is not stable")
	}
}

func TestFreshShapeScopeCapturesExplicitBinders(t *testing.T) {
	a := &Analyzer{}
	scopeKey := func(fn *ast.FuncDecl) string {
		restore := a.enterFreshShapeScope(fn, nil, "body")
		defer restore()
		return a.freshShapeScope
	}
	base := &ast.FuncDecl{Name: "owner"}
	baseKey := scopeKey(base)
	cases := map[string]func(*ast.FuncDecl){
		"type parameter":       func(fn *ast.FuncDecl) { fn.TypeParams = []string{"T"} },
		"region parameter":     func(fn *ast.FuncDecl) { fn.RegionParams = []string{"r"} },
		"permission parameter": func(fn *ast.FuncDecl) { fn.PermissionParams = []string{"p"} },
		"return region":        func(fn *ast.FuncDecl) { fn.ReturnRegion = "r" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fn := *base
			change(&fn)
			if got := scopeKey(&fn); got == baseKey {
				t.Fatal("different binder scope shares FreshShape owner identity")
			}
		})
	}
}

func TestInferredLambdasFromDifferentBodiesKeepFreshReturnsDistinct(t *testing.T) {
	const src = `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def first(array: darray[i32, row]) -> void:
    f = fn() => resize(array, 8)

def second(array: darray[i32, row]) -> void:
    f = fn() => resize(array, 8)
`
	for _, workers := range []string{"1", "2", "4"} {
		t.Run(workers, func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_BODIES", workers)
			result := analyzeTreeTestSourceWithSemanticErrors(t, "fresh_lambda.elisa", src)
			if len(result.Errors()) != 0 {
				t.Fatal(result.Errors())
			}
			var shapes []*FreshShape
			for expr, typ := range result.ExprTypes {
				if _, ok := expr.(*ast.LambdaExpr); !ok {
					continue
				}
				fn, ok := typ.(*FuncType)
				if !ok {
					t.Fatalf("lambda type %T", typ)
				}
				got := collectFreshShapesInType(fn.Return)
				if len(got) != 1 {
					t.Fatalf("inferred lambda lost fresh return: %#v", fn.Return)
				}
				shapes = append(shapes, got[0])
			}
			if len(shapes) != 2 || SameShape(shapes[0], shapes[1]) {
				t.Fatalf("independent inferred lambda returns equated: %#v", shapes)
			}
		})
	}
}

func TestFreshShapeRetainedLineShiftMatchesCold(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "2")
	before := `def marker() -> i64:
    return 0
extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]
def grow(array: darray[i32, row]) -> darray[i32, shape_after]:
    return resize(array, 8)
def same(left: darray[i32, shape_pair], right: darray[i32, shape_pair]) -> void:
    pass
def bad(array: darray[i32, row]) -> void:
    same(grow(array), grow(array))
`
	after := strings.Replace(before, "    return 0", "    value: i64 = 0\n    return value", 1)
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	updated, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("retained differs from cold:\n%s\n===\n%s", got, want)
	}
}

func TestFreshShapeHookOwnerIgnoresLinkerLineSuffix(t *testing.T) {
	fn := &ast.FuncDecl{Name: "__cast__"}
	sym := &Symbol{Name: "Owner_cast__L10_C1", Kind: SymbolFunc, Node: fn, Type: &FuncType{Name: "Owner.__cast__"}}
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: sym}}
	restore := a.enterFreshShapeScope(fn, nil, "body")
	first := a.freshShapeScope
	restore()
	fn.Position.Line = 20
	sym.Name = "Owner_cast__L20_C1"
	restore = a.enterFreshShapeScope(fn, nil, "body")
	defer restore()
	if first != a.freshShapeScope {
		t.Fatal("hook identity changes when source lines shift")
	}
}
