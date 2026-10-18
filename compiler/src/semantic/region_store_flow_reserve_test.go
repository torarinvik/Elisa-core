package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestStoreFlowReserveDoesNotRetainCapacitySources(t *testing.T) {
	src := `struct LookupContext:
    lookup_key_scratch: mutable darray[u8] = []

def encode_into(module_name: sview, name: sview, key: mutable darray[u8]&) -> void:
    key.reserve(12 + module_name.len + name.len)
    key.extend(module_name)
    key.extend(name)

def lookup(bound: darray[sview]&, context: mutable LookupContext&) -> void:
    encode_into("", bound[0], &context.lookup_key_scratch)

def encode(module_name: sview, name: sview) -> darray[u8]:
    key: mutable darray[u8] = []
    key.reserve(12 + module_name.len + name.len)
    key.extend(module_name)
    key.extend(name)
    return key

def reserve_only(source: sview, target: mutable darray[sview]&) -> void:
    target.reserve(source.len)

def store_and_count(source: sview, target: mutable darray[sview]&) -> usize:
    target.push(source)
    return 1

def reserve_effect(source: sview, target: mutable darray[sview]&) -> void:
    target.reserve(store_and_count(source, target))

def store_view(source: sview, target: mutable darray[sview]&) -> void:
    target.push(source)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "reserve_store_flow.elisa", src, AnalyzeOptions{})
	for _, name := range []string{"encode", "encode_into", "lookup", "reserve_only", "store_view", "reserve_effect"} {
		s := r.analyzer.storeFlowSummaryFor(storeScanFindFunc(t, r, name))
		if s == nil {
			t.Fatalf("%s has no summary", name)
		}
		switch name {
		case "encode":
			if s.retConn[0] || s.retConn[1] {
				t.Errorf("byte-copy encoding retains capacity sources: %v", s.retConn)
			}
		case "encode_into":
			if !s.writtenParam(2) || s.reaches(0, 2) || s.reaches(1, 2) {
				t.Errorf("encoding must not retain input views: %+v", s)
			}
		case "lookup":
			if !s.writtenParam(1) || s.reaches(0, 1) {
				t.Errorf("bound element must not reach lookup context through capacity: %+v", s)
			}
		case "reserve_only":
			if !s.writtenParam(1) || s.reaches(0, 1) {
				t.Errorf("reserve must mutate receiver without retaining capacity source: %+v", s)
			}
		case "store_view", "reserve_effect":
			if !s.reaches(0, 1) {
				t.Fatal("pushed view lost source flow")
			}
		}
	}
}

func TestStoreFlowReserveRequiresBuiltinReceiver(t *testing.T) {
	for _, tc := range []struct {
		name   string
		proven bool
		ufcs   Type
		want   bool
	}{
		{"builtin", true, nil, true},
		{"unknown", false, nil, false},
		{"generic_ufcs", true, &TypeParamType{Name: "T"}, false},
		{"darray_ufcs", true, &DArrayType{Elem: &BuiltinType{Name: "u8"}}, false},
		{"unrelated_ufcs", true, &BuiltinType{Name: "u32"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "target"}, Field: "reserve"}, Args: []ast.Expr{&ast.Ident{Name: "capacity"}}}
			a := &Analyzer{storeFlowDarrays: map[string]bool{"target": tc.proven}}
			if tc.ufcs != nil {
				a.ufcsFunctionsByName = map[string][]*Symbol{"Custom.reserve": {{Type: &FuncType{Params: []Type{tc.ufcs}}}}}
			}
			c := &storeFlowCtx{written: map[string]bool{"target": true}}
			ret, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
			if handled != tc.want {
				t.Fatalf("handled=%v want %v", handled, tc.want)
			}
			if handled && (len(c.edges) != 0 || len(ret) != 1 || ret[0] != "target") {
				t.Fatalf("reserve must preserve only receiver result: edges=%v result=%v", c.edges, ret)
			}
		})
	}
}

func TestStoreFlowReserveDoesNotHideUnknownCapacityCall(t *testing.T) {
	capacity := &ast.CallExpr{Func: &ast.Ident{Name: "unknown"}, Args: []ast.Expr{&ast.Ident{Name: "source"}}}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "target"}, Field: "reserve"}, Args: []ast.Expr{capacity}}
	a := &Analyzer{storeFlowDarrays: map[string]bool{"target": true}}
	if _, handled := a.applyCallStoreFlow(call, &storeFlowCtx{}, map[*ast.CallExpr][]string{}); handled {
		t.Fatal("reserve must not hide an unsummarized capacity call")
	}
}
