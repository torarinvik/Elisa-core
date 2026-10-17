//go:build cgo

package semantic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestFunctionScalarFactsRealAnalysisAndOwnedRestore(t *testing.T) {
	const source = `def work(value: i64) -> i64:
    return value + 2
`
	fn := parseProductFunction(t, source)
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	result := Analyze(&ast.File{Filename: "fixture.elisa", Decls: []ast.Decl{fn}})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	input := FunctionScalarObservations{ExprTypes: result.ExprTypes, ResolvedTypeNames: result.ResolvedTypeNames, ResolvedValueNames: result.ResolvedValueNames}
	facts, err := CaptureFunctionScalarFacts(index, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := CaptureFunctionScalarFacts(index, input)
	if err != nil || again.Encoded() != facts.Encoded() {
		t.Fatalf("non-deterministic capture: %v", err)
	}
	// The payload must not observe later mutation of input maps or semantic types.
	for _, typ := range input.ExprTypes {
		if builtin, ok := typ.(*BuiltinType); ok {
			builtin.Name = "u32"
		}
	}
	clear(input.ExprTypes)
	stored, err := DecodeFunctionScalarFacts(facts.Encoded())
	if err != nil {
		t.Fatal(err)
	}
	fresh := parseProductFunction(t, "\n\n"+source)
	target, err := IndexFunctionSource(fresh, productContext())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := stored.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	cold := Analyze(&ast.File{Filename: "fixture.elisa", Decls: []ast.Decl{fresh}})
	if len(cold.Errors()) != 0 {
		t.Fatal(cold.Errors())
	}
	if !reflect.DeepEqual(restored.ExprTypes, cold.ExprTypes) || !reflect.DeepEqual(restored.ResolvedTypeNames, cold.ResolvedTypeNames) || !reflect.DeepEqual(restored.ResolvedValueNames, cold.ResolvedValueNames) {
		t.Fatal("restored observations differ from cold analysis")
	}
	for node, typ := range restored.ExprTypes {
		if typ == cold.ExprTypes[node] {
			t.Fatal("restored type aliases another invocation")
		}
		if node.Pos().Line < 3 {
			t.Fatal("stale source position")
		}
	}
	// Each restore owns independent types/maps as well as current source nodes.
	for _, typ := range restored.ExprTypes {
		if builtin, ok := typ.(*BuiltinType); ok {
			builtin.Name = "u8"
		}
	}
	second, err := stored.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.ExprTypes, cold.ExprTypes) {
		t.Fatal("first restore mutated stored product")
	}
}

func TestFunctionScalarFactsTypedPresenceAndAtomicFailure(t *testing.T) {
	const source = `def work(value: i64) -> i64:
    return value + 2
`
	fn := parseProductFunction(t, source)
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	expr := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	ident := expr.Left.(*ast.Ident)
	facts, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{
		ExprTypes:          map[ast.Expr]Type{ident: &BitIntType{Signed: true, Width: 7}},
		ResolvedValueNames: map[*ast.Ident]string{ident: "M.value"},
		PermGrowthOps:      map[ast.Expr]bool{expr: false}, GhostContracts: map[ast.Expr]bool{ident: true},
		RewriteDefaults: map[*ast.Ident]bool{ident: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := parseProductFunction(t, source)
	target, _ := IndexFunctionSource(fresh, productContext())
	restored, err := facts.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	current := fresh.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	if flag, ok := restored.PermGrowthOps[current]; !ok || flag {
		t.Fatal("false entry lost")
	}
	if restored.ResolvedValueNames[current.Left.(*ast.Ident)] != "M.value" {
		t.Fatal("resolved identity lost")
	}
	var payload scalarFactsPayload
	if err := json.Unmarshal([]byte(facts.Encoded()), &payload); err != nil {
		t.Fatal(err)
	}
	for i := range payload.Entries {
		if payload.Entries[i].Field == "RewriteDefaults" {
			payload.Entries[i].Field = "IndexBoundsProven"
		}
	}
	bad, _ := json.Marshal(payload)
	decoded, err := DecodeFunctionScalarFacts(string(bad))
	if err != nil {
		t.Fatal(err)
	}
	partial, err := decoded.Restore(target)
	if err == nil || !reflect.DeepEqual(partial, FunctionScalarObservations{}) {
		t.Fatal("invalid typed anchor exposed partial observations")
	}
}

func TestFunctionScalarFactsDeclineUnsupportedInputs(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	expr := fn.Body[0].(*ast.ReturnStmt).Value
	for _, typ := range []Type{nil, (*BuiltinType)(nil), &StructType{Name: "SameName"}, &FuncType{}, &BuiltinType{Name: "future_builtin"}, &BitIntType{Width: 65}} {
		if _, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{expr: typ}}); err == nil {
			t.Fatalf("unsupported type accepted: %T", typ)
		}
	}
	for _, encoded := range []string{"", `{}`, `{"Version":2}`, `{"Version":1,"unknown":true}`, `{"Version":1} {}`, strings.Repeat("x", functionScalarFactsMaxBytes+1), `{"Version":1,"Entries":[{"Field":"future"}]}`} {
		if _, err := DecodeFunctionScalarFacts(encoded); err == nil {
			t.Fatalf("malformed product accepted: %.80s", encoded)
		}
	}
	if _, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{&ast.IntLit{Value: "7"}: &BuiltinType{Name: "i64"}}}); err == nil {
		t.Fatal("foreign/generated node accepted")
	}
	ident := &ast.Ident{Name: "oversized"}
	if _, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ResolvedValueNames: map[*ast.Ident]string{ident: strings.Repeat("<", functionScalarFactsMaxBytes)}}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("oversized string did not decline before source traversal: %v", err)
	}
}

func TestFunctionScalarObservationSchemaPinsNamesAndTypes(t *testing.T) {
	typ := reflect.TypeOf(FunctionScalarObservations{})
	fields := make([]reflect.StructField, typ.NumField())
	for i := range fields {
		fields[i] = typ.Field(i)
	}
	fields[0].Type = reflect.TypeOf(map[ast.Expr]string(nil))
	if err := validateScalarObservationSchema(reflect.StructOf(fields)); err == nil {
		t.Fatal("same-count field type change admitted")
	}
	fields[0] = typ.Field(0)
	fields[0].Name = "FutureExprTypes"
	if err := validateScalarObservationSchema(reflect.StructOf(fields)); err == nil {
		t.Fatal("same-count field rename admitted")
	}
}

func TestFunctionScalarFactsRejectCorruptValidEnvelope(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	expr := fn.Body[0].(*ast.ReturnStmt).Value
	facts, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{expr: &BuiltinType{Name: "i64"}}})
	if err != nil {
		t.Fatal(err)
	}
	var original scalarFactsPayload
	if err := json.Unmarshal([]byte(facts.Encoded()), &original); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*scalarFactsPayload){
		func(p *scalarFactsPayload) { p.Version++ },
		func(p *scalarFactsPayload) { p.Entries = append(p.Entries, p.Entries[0]) },
		func(p *scalarFactsPayload) { p.Entries[0].Field = "future_field" },
		func(p *scalarFactsPayload) { p.Entries[0].Type = &scalarTypeValue{Kind: "bitint", Width: 65} },
		func(p *scalarFactsPayload) {
			p.Entries[0].Type = &scalarTypeValue{Kind: "builtin", Name: "i64", Signed: true}
		},
		func(p *scalarFactsPayload) { p.Entries[0].Flag = true },
	} {
		p := original
		p.Entries = append([]scalarFactEntry(nil), original.Entries...)
		mutate(&p)
		data, _ := json.Marshal(p)
		if _, err := DecodeFunctionScalarFacts(string(data)); err == nil {
			t.Fatalf("invalid entry accepted: %s", data)
		}
	}
	for _, typ := range []Type{&NeverType{}, &NullType{}, &BitIntType{Signed: false, Width: 1}} {
		facts, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{expr: typ}})
		if err != nil {
			t.Fatal(err)
		}
		output, err := facts.Restore(index)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(output.ExprTypes[expr], typ) {
			t.Fatalf("leaf type lost: %T", typ)
		}
	}
}

func TestFunctionScalarFactsEmptyProductStillValidatesInputs(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	facts, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.Restore(index); err != nil {
		t.Fatal(err)
	}
	changed, _ := IndexFunctionSource(parseProductFunction(t, strings.Replace(functionProductSource, "value + 1", "value + 2", 1)), productContext())
	otherScope := productContext()
	otherScope.Origin = "/other/input.elisa"
	other, _ := IndexFunctionSource(parseProductFunction(t, functionProductSource), otherScope)
	for _, target := range []*FunctionSourceIndex{nil, changed, other} {
		if _, err := facts.Restore(target); err == nil {
			t.Fatal("empty product bypassed function input validation")
		}
	}
}
