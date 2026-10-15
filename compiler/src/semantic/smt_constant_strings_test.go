//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestSMTConstantStringContracts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"returns", "return \"..=\" if code == 109\n    return \"\"", false},
		{"match", "return match code:\n        109: \"..=\"\n        _: \"\"", false},
		{"wrong_match", "return match code:\n        108: \"..=\"\n        _: \"\"", true},
		{"wrong_return", "return \"\"", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, tc.name+".elisa", "def name(code: u64) -> sview:\n    ensure code == 109 implies result == \"..=\"\n    "+tc.body+"\n")
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTStringSelectorClassifierResultContracts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		outputForI8 string
		reject      bool
	}{
		{name: "distinct_groups", outputForI8: "i32"},
		{name: "wrong_cross_group", outputForI8: "i64", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `def wasm_scalar_kind(type_text: sview) -> sview:
    ensure (result == "i32") == (type_text == "i8" or type_text == "u8")
    ensure (result == "i64") == (type_text == "i64")
    ensure (result == "") == (type_text != "i8" and type_text != "u8" and type_text != "i64")
    return when type_text:
        "i8" | "u8" -> "` + tc.outputForI8 + `"
        "i64" -> "i64"
        _ -> ""
`
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTStringSelectorClassifierMultipleReturns(t *testing.T) {
	for _, tc := range []struct {
		name, selected, capability string
		reject                     bool
	}{
		{name: "matching", selected: "yes", capability: "Abort.Panic"},
		{name: "wrong_selector", selected: "no", capability: "Abort.Panic", reject: true},
		{name: "write_capability", selected: "yes", capability: "Memory.Write", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `def classify(text: sview) -> bool can[` + tc.capability + `]:
    ensure result == (text == "yes")
    can Abort.Panic:
        return true if text == "` + tc.selected + `"
        return false
`
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestClassifierStringFrameStableRejectsInferredWrite(t *testing.T) {
	decl := &ast.FuncDecl{Permissions: []ast.PermissionRef{{Name: "Abort", Member: "Panic"}}}
	a := &Analyzer{currentFunctionUsedPermissionRefs: []ast.PermissionRef{{Name: "Memory", Member: "Write"}}}
	if classifierStringFrameStable(a, decl) {
		t.Fatal("classifier string read should be rejected after an inferred memory write")
	}
}

func TestSMTConstantStringNamedTupleResultFields(t *testing.T) {
	for _, tc := range []struct {
		name, binding, wasmType string
		reverse                 bool
		reject                  bool
	}{
		{name: "exact", binding: "string", wasmType: "i32"},
		{name: "reversed_exact", binding: "string", wasmType: "i32", reverse: true},
		{name: "wrong_binding", binding: "pointer", wasmType: "i32", reject: true},
		{name: "reversed_wrong_binding", binding: "pointer", wasmType: "i32", reverse: true, reject: true},
		{name: "wrong_wasm_type", binding: "string", wasmType: "i64", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindingCheck := `result.binding == "string"`
			if tc.reverse {
				bindingCheck = `"string" == result.binding`
			}
			source := `def classify() -> (binding: sview, wasm_type: sview):
    ensure ` + bindingCheck + `
    ensure result.wasm_type == "i32"
    return ("` + tc.binding + `", "` + tc.wasmType + `")
`
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTConstantStringSelectorMatchUsesDistinctLiteralIDs(t *testing.T) {
	selector := &ast.Ident{Name: "type_text"}
	match := &ast.MatchExpr{Value: selector, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchStringLiteralPattern{Value: "i8"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "i32"}}}},
		{Pattern: &ast.MatchStringLiteralPattern{Value: "i64"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "i64"}}}},
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: ""}}}},
	}}
	function := &ast.FuncDecl{
		Name:   "wasm_scalar_kind",
		Params: []ast.ParamDecl{{Name: "type_text"}},
		Body:   []ast.Stmt{&ast.ReturnStmt{Value: match}},
	}
	analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
	analyzer.currentScope.Define(&Symbol{Name: "type_text", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0})
	translator := &smtTranslator{a: analyzer}

	term, ok := translator.constantStringEquality(match, &ast.StringLit{Value: "i64"}, nil, 0)
	want := "(ite (= aux_1 2) false (ite (= aux_1 1) true false))"
	if !ok || term != want {
		t.Fatalf("string classifier term = %q, %v; want %q", term, ok, want)
	}
	if len(translator.auxVars) != 1 || len(translator.auxDecls) != 1 || translator.auxVars[0] != "aux_1" {
		t.Fatalf("string parameter encoding declarations = vars %v, decls %v", translator.auxVars, translator.auxDecls)
	}
}

func TestSMTConstantStringSelectorMatchDeclinesUnsupportedParams(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mutable         bool
		param           bool
		calleeBodyDepth int
		env             map[string]string
	}{
		{name: "mutable", mutable: true, param: true},
		{name: "local"},
		{name: "substituted", param: true, env: map[string]string{"type_text": "v_type_text"}},
		{name: "callee_body", param: true, calleeBodyDepth: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := &ast.Ident{Name: "type_text"}
			match := &ast.MatchExpr{Value: selector, Arms: []ast.MatchArm{
				{Pattern: &ast.MatchStringLiteralPattern{Value: "i8"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "i32"}}}},
				{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: ""}}}},
			}}
			function := &ast.FuncDecl{
				Name: "wasm_scalar_kind",
				Body: []ast.Stmt{&ast.ReturnStmt{Value: match}},
			}
			analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
			if tc.param {
				function.Params = []ast.ParamDecl{{Name: "type_text", Mutable: tc.mutable}}
				analyzer.currentScope.Define(&Symbol{Name: "type_text", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0, Mutable: tc.mutable})
			} else {
				analyzer.currentScope.Define(&Symbol{Name: "type_text", Kind: SymbolLocal, Type: &SViewType{}})
			}
			translator := &smtTranslator{a: analyzer, calleeBodyDepth: tc.calleeBodyDepth}
			if term, ok := translator.constantStringEquality(match, &ast.StringLit{Value: "i32"}, tc.env, 0); ok {
				t.Fatalf("unsupported string match translated as %q", term)
			}
			if len(translator.auxDecls) != 0 {
				t.Fatalf("declined string match left SMT declarations: %v", translator.auxDecls)
			}
		})
	}
}

func TestSMTConstantStringSelectorMatchDeclinesEmbeddedNUL(t *testing.T) {
	match := &ast.MatchExpr{Value: &ast.Ident{Name: "type_text"}, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchStringLiteralPattern{Value: "i8\x00tail"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "i32"}}}},
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: ""}}}},
	}}
	if constantStringResult(match, 0) {
		t.Fatal("NUL string selector pattern accepted")
	}
}

func TestSMTConstantStringBindingsAreEnvironmentScoped(t *testing.T) {
	a := &Analyzer{}
	tr := &smtTranslator{a: a}
	first := map[string]string{}
	second := map[string]string{}
	literal := &ast.StringLit{Value: "first"}
	a.bindSMTEnvArg(tr, first, "value", literal, nil, true)
	id := &ast.Ident{Name: "value"}
	if term, ok := tr.constantStringEquality(id, literal, first, 0); !ok || term != "true" {
		t.Fatalf("first binding: %q %v", term, ok)
	}
	if term, ok := tr.constantStringEquality(id, literal, second, 0); ok {
		t.Fatalf("binding leaked: %q", term)
	}
	a.bindSMTEnvArg(tr, second, "value", &ast.StringLit{Value: "second"}, nil, true)
	if term, ok := tr.constantStringEquality(id, literal, second, 0); !ok || term != "false" {
		t.Fatalf("second binding: %q %v", term, ok)
	}
	if term, ok := tr.constantStringEquality(id, literal, first, 0); !ok || term != "true" {
		t.Fatalf("caller changed: %q %v", term, ok)
	}
}

func TestSMTConstantStringComparisonDeclinesSymbolicPair(t *testing.T) {
	a := &Analyzer{}
	tr := &smtTranslator{a: a}
	env := map[string]string{}
	a.bindSMTEnvArg(tr, env, "left", &ast.StringLit{Value: "same"}, nil, true)
	a.bindSMTEnvArg(tr, env, "right", &ast.StringLit{Value: "different"}, nil, true)
	if term, ok := tr.constantStringEquality(&ast.Ident{Name: "left"}, &ast.Ident{Name: "right"}, env, 0); ok {
		t.Fatalf("unsupported two-environment comparison accepted: %q", term)
	}
}

func TestSMTConstantStringFragmentRejectsDynamicPatterns(t *testing.T) {
	for _, pattern := range []*ast.MatchLiteralPattern{
		{Value: &ast.Ident{Name: "runtime"}},
		{Value: &ast.IntLit{}, Pinned: true},
	} {
		match := &ast.MatchExpr{Value: &ast.Ident{Name: "code"}, Arms: []ast.MatchArm{
			{Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "yes"}}}},
			{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "no"}}}},
		}}
		if constantStringResult(match, 0) {
			t.Fatal("dynamic/pinned selector accepted")
		}
	}
}

func TestSMTConstantStringFragmentRejectsCallSubject(t *testing.T) {
	match := &ast.MatchExpr{Value: &ast.CallExpr{}, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "yes"}}}},
	}}
	if constantStringResult(match, 0) {
		t.Fatal("call subject accepted")
	}
}

func TestSMTConstantStringEqualityDeclinesEmbeddedNUL(t *testing.T) {
	tr := &smtTranslator{}
	prefix := &ast.StringLit{Value: "a"}
	terminated := &ast.StringLit{Value: "a\x00tail"}
	for _, operands := range [][2]ast.Expr{{prefix, terminated}, {terminated, prefix}} {
		if term, ok := tr.constantStringEquality(operands[0], operands[1], nil, 0); ok {
			t.Fatalf("NUL comparison accepted: %q", term)
		}
	}
	if constantStringResult(terminated, 0) {
		t.Fatal("NUL literal binding accepted")
	}
}

func TestSMTConstantStringNestedNamesDoNotProveFalseEquality(t *testing.T) {
	result := analyzeContractStrict(t, "nested_string_names.elisa", `def same(x: sview, y: sview) -> bool:
    return x == y

def forward(x: sview, y: sview) -> bool:
    return same("a", x)

def claim() -> bool:
    ensure result == true
    return forward("b", "a")
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false nested equality claim was not rejected: %v", result.Errors())
	}
}

func TestSMTConstantStringNarrowSelectorPatterns(t *testing.T) {
	for _, tc := range []struct {
		name, width, pattern, output, diagnostic string
	}{
		{"u8_overflow", "u8", "256", "wrong", "does not fit in u8"},
		{"u16_overflow", "u16", "65536", "wrong", "does not fit in u16"},
		{"u8_range_overflow", "u8", "256..<257", "wrong", "does not fit in u8"},
		{"u16_range_overflow", "u16", "65536..<65537", "wrong", "does not fit in u16"},
		{"u8_wrong_result", "u8", "0", "wrong", "could not be proven"},
		{"u16_wrong_result", "u16", "0", "wrong", "could not be proven"},
		{"u8_valid", "u8", "0", "right", ""},
		{"u16_valid", "u16", "0", "right", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def name(code: " + tc.width + ") -> sview:\n    ensure code == 0 implies result == \"right\"\n    return match code:\n        " + tc.pattern + ": \"" + tc.output + "\"\n        _: \"right\"\n"
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			diagnostics := allDiagnostics(result)
			if tc.diagnostic != "" {
				if !strings.Contains(diagnostics, tc.diagnostic) {
					t.Fatalf("missing %q: %s", tc.diagnostic, diagnostics)
				}
			} else if len(result.Errors()) != 0 {
				t.Fatalf("valid narrow pattern rejected: %s", diagnostics)
			}
		})
	}
}

// Stage1's ABI classifier shape: string-literal tuple returns behind guards that call a pure
// (single-return, immutable-formal) helper. The frame stays stable: nothing between the guards
// and the return can write the borrowed view.
const classifierFrameHelper = `
def hs(t: sview) -> bool:
    ensure result == (t.len > 0 and t[t.len - 1] == 63)
    return t.len > 0 and t[t.len - 1] == 63
`

func TestSMTStringClassifierLiteralTupleReturnsAndPureCalls(t *testing.T) {
	src := classifierFrameHelper + `
def scalar(t: sview) -> sview:
    ensure (result == "string") == (t == "cstr")
    return "string" if t == "cstr"
    return ""

def pair(t: sview) -> (binding: sview, w: sview):
    ensure (result.binding == "string") == (t == "cstr")
    return ("string", "i32") if t == "cstr"
    return ("", "")

def guarded(t: sview) -> (binding: sview, w: sview):
    ensure (result.binding == "string") == (not hs(t) and t == "cstr")
    if hs(t):
        return ("", "")
    return ("string", "i32") if t == "cstr"
    return ("", "")
`
	result := analyzeContractStrict(t, "classifier_frame.elisa", src)
	for _, fn := range []string{"scalar", "pair", "guarded"} {
		if n := ensureFailuresFor(result.Errors(), fn); n != 0 {
			t.Fatalf("%s must prove; got: %v", fn, result.Errors())
		}
	}
}

func TestSMTStringClassifierDeclinesFalseClaimsAndImpureFrames(t *testing.T) {
	src := classifierFrameHelper + `
def wrong_literal(t: sview) -> (binding: sview, w: sview):
    ensure (result.binding == "string") == (t == "cstrx")
    return ("string", "i32") if t == "cstr"
    return ("", "")

def wrong_guard(t: sview) -> (binding: sview, w: sview):
    ensure (result.binding == "string") == (t == "cstr")
    if hs(t):
        return ("string", "i32")
    return ("string", "i32") if t == "cstr"
    return ("", "")

def tick(n: mutable i64&) -> bool:
    n += 1
    return n > 0

def impure_guard(t: sview, n: mutable i64&) -> (binding: sview, w: sview):
    ensure (result.binding == "string") == (t == "cstr")
    if tick(n) and false:
        return ("", "")
    return ("string", "i32") if t == "cstr"
    return ("", "")
`
	result := analyzeContractStrict(t, "classifier_frame_neg.elisa", src)
	// wrong_literal: "cstr" and "cstrx" are distinct literals. wrong_guard: t = "a?" returns
	// "string" without being "cstr". impure_guard: tick writes through a mutable formal, so the
	// frame gate must decline even though the claim happens to hold.
	for _, fn := range []string{"wrong_literal", "wrong_guard", "impure_guard"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s must stay unproven; got: %v", fn, result.Errors())
		}
	}
}

// String identities of write-free sview-returning calls: an immutable local bound to the call,
// projected through a returned tuple, carries the callee's verified literal-equality contracts.
// The callee lives in another module and is called qualified AFTER the earlier return points, so
// the frame gate must resolve callees it has not analyzed yet (stage1's WASM ABI classifier).
const stringCallIDCallee = `
module Scalars:
    public:
        def wt(t: sview) -> sview can[Abort.Panic]:
            ensure (result == "f32") == (t == "f32")
            ensure (result == "i32") == (t == "i8" or t == "u8")
            ensure (result == "") == (not (t == "f32" or t == "i8" or t == "u8"))
            can Abort.Panic:
                return when t:
                    "f32" -> "f32"
                    "i8" | "u8" -> "i32"
                    _ -> ""
`

func TestSMTStringCallResultIdentity(t *testing.T) {
	src := stringCallIDCallee + `
def classify(t: sview) -> (binding: sview, w: sview) can[Abort.Panic]:
    ensure (result.w == "f32") == (result.binding == "scalar" and t == "f32")
    ensure (result.w == "i32") == (result.binding == "scalar" and (t == "i8" or t == "u8"))
    ensure (result.binding == "") == (result.w == "")
    can Abort.Panic:
        if t == "cstr":
            return ("", "")
        s: sview = Scalars::wt(t)
        return ("scalar", s) if s != ""
        return ("", "")
`
	result := analyzeContractStrict(t, "string_call_id.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "classify"); n != 0 {
		t.Fatalf("call-result string identity must prove classify; got: %v", result.Errors())
	}
}

func TestSMTStringCallResultIdentityDeclines(t *testing.T) {
	src := stringCallIDCallee + `
def wrong(t: sview) -> (binding: sview, w: sview) can[Abort.Panic]:
    ensure (result.w == "f32") == (t == "i8")
    can Abort.Panic:
        s: sview = Scalars::wt(t)
        return ("scalar", s)

def other_arg(t: sview, u: sview) -> (binding: sview, w: sview) can[Abort.Panic]:
    ensure (result.w == "f32") == (u == "f32")
    can Abort.Panic:
        s: sview = Scalars::wt(t)
        return ("scalar", s)

def wt_writer(t: sview, n: mutable i64&) -> sview can[Abort.Panic]:
    ensure (result == "f32") == (t == "f32")
    n += 1
    return "f32" if t == "f32"
    return ""

def writer_callee(t: sview, k: mutable i64&) -> (binding: sview, w: sview) can[Abort.Panic]:
    ensure (result.w == "f32") == (t == "f32")
    can Abort.Panic:
        s: sview = wt_writer(t, k)
        return ("scalar", s)
`
	result := analyzeContractStrict(t, "string_call_id_neg.elisa", src)
	// wrong: the callee contract relates "f32" to t == "f32", not "i8". other_arg: the callee
	// formal t must bind to the ARGUMENT t, never to another caller param. writer_callee: a callee
	// that writes through a mutable formal is not write-free, so its result gets no identity (the
	// claim holds, but the frame gate must decline).
	for _, fn := range []string{"wrong", "other_arg", "writer_callee"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s must stay unproven; got: %v", fn, result.Errors())
		}
	}
}
