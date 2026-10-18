package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const sviewRecordElementTypes = `struct Metadata:
    text: sview
struct Instruction:
    op: sview
    metadata: Metadata
struct Program:
    instructions: darray[Instruction]
`

func TestSMTSViewRecordElementFieldProjectionCongruence(t *testing.T) {
	cases := []string{
		`def same_nested(instructions: darray[Instruction], i: usize, j: usize) -> bool:
    requires i < instructions.count and j < instructions.count and i == j
    ensure result == true
    return instructions[i].metadata.text == instructions[j].metadata.text
`,
		`def same_struct_field(program: Program, i: usize, j: usize) -> bool:
    requires i < program.instructions.count and j < program.instructions.count and i == j
    ensure result == true
    return program.instructions[i].metadata.text == program.instructions[j].metadata.text
`,
		`def same_immutable_alias(instructions: darray[Instruction], i: usize) -> bool:
    requires i < instructions.count
    ensure result == true
    selected: Instruction = instructions[i]
    return selected.metadata.text == instructions[i].metadata.text
`,
	}
	for _, source := range cases {
		result := analyzeContractStrict(t, "sview_record_elements.elisa", sviewRecordElementTypes+source)
		if len(result.Errors()) != 0 {
			t.Fatalf("same indexed sview field should be congruent:\n%s", allDiagnostics(result))
		}
	}
}

func TestSMTSViewRecordElementFieldsDoNotOverclaim(t *testing.T) {
	result := analyzeContractStrict(t, "sview_record_elements_wrong.elisa", sviewRecordElementTypes+`def distinct_indices_may_differ(instructions: darray[Instruction], i: usize, j: usize) -> bool:
    requires i < instructions.count and j < instructions.count and i != j
    ensure result == true
    return instructions[i].metadata.text == instructions[j].metadata.text

def out_of_bounds_view_is_unknown(instructions: darray[Instruction]) -> bool:
    requires instructions.count == 0
    ensure result == true
    return instructions[0].metadata.text.len == 0
`)
	for _, name := range []string{"distinct_indices_may_differ", "out_of_bounds_view_is_unknown"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s must remain unproven:\n%s", name, allDiagnostics(result))
		}
	}
}

func TestSMTSViewFieldClassifierCallOnIndexedRecord(t *testing.T) {
	result := analyzeContractStrict(t, "sview_record_label_call.elisa", sviewRecordElementTypes+`def label_name(instruction: Instruction) -> sview:
    ensure result == (instruction.metadata.text if instruction.op == "label" else "")
    return instruction.metadata.text if instruction.op == "label" else ""

def is_indexed_label(instructions: darray[Instruction], index: usize) -> bool:
    ensure result == (label_name(instructions[index]) == "label")
    return label_name(instructions[index]) == "label"
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("indexed struct argument should preserve helper-call identity:\n%s", allDiagnostics(result))
	}
}

func TestSMTSViewRecordLabelLookupBranchToReturn(t *testing.T) {
	result := analyzeContractStrict(t, "sview_record_label_lookup.elisa", `struct LabelInstruction:
    op: sview
    text: sview

def label_index(instructions: darray[LabelInstruction], target: sview) -> i64:
    ensure result >= -1
    ensure (instructions.count.i64() < 0 or instructions.count.i64().usize() != instructions.count) implies result == -1
    ensure result >= 0 implies result < instructions.count.i64()
    ensure result >= 0 implies (instructions[result.usize()].op == "label" and instructions[result.usize()].text == target)
    ensure result >= 0 implies (forall prior in 0..<result: not (instructions[prior.usize()].op == "label" and instructions[prior.usize()].text == target))
    ensure (instructions.count.i64() >= 0 and instructions.count.i64().usize() == instructions.count and result == -1) implies (forall candidate in 0..<instructions.count.i64(): not (instructions[candidate.usize()].op == "label" and instructions[candidate.usize()].text == target))
    count: usize = instructions.count
    count_signed: i64 = count.i64()
    return -1 if count_signed < 0 or count_signed.usize() != count
    for index in 0..<count |instructions, target|:
        invariant index <= count
        invariant forall prior in 0..<index: prior < count and not (instructions[prior].op == "label" and instructions[prior].text == target)
        if instructions[index].op == "label":
            if instructions[index].text == target:
                return index.i64()
    return -1
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("label lookup's branch facts should reach the returned indexed record:\n%s", allDiagnostics(result))
	}
}

func TestSMTQuantifiedIndexedSViewCallDeclinesWithoutBinderEscape(t *testing.T) {
	source := sviewRecordElementTypes + `def same_view(view: sview) -> sview:
    ensure result == view
    return view

def classify(items: darray[Instruction], index: usize) -> bool:
    return same_view(items[index].metadata.text) == "label"
`
	result := analyzeFunctionAnalysisTestSource(t, "quantified_indexed_sview_call.elisa", source)
	fnSymbol, ok := result.GlobalScope.Lookup("classify")
	if !ok || fnSymbol == nil {
		t.Fatal("classify function is missing")
	}
	fn, ok := fnSymbol.Node.(*ast.FuncDecl)
	if !ok || fn == nil {
		t.Fatal("classify declaration is not a function")
	}
	fnType, ok := fnSymbol.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) != 2 {
		t.Fatal("classify function signature is missing its parameters")
	}
	ret, ok := fn.Body[len(fn.Body)-1].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		t.Fatal("classify return expression is missing")
	}
	analyzer := result.analyzer
	analyzer.currentFuncDecl = fn
	analyzer.currentScope = NewScope(analyzer.globalScope)
	analyzer.currentScope.Define(&Symbol{Name: "items", Kind: SymbolParam, Type: fnType.Params[0], Node: fn, ParamIndex: 0})
	analyzer.currentScope.Define(&Symbol{Name: "index", Kind: SymbolParam, Type: fnType.Params[1], Node: fn, ParamIndex: 1})
	tr := analyzer.newSMTTranslator(nil)
	if _, ok := tr.boolTerm(ret.Value, map[string]string{"index": "q_sview_index"}); ok {
		t.Fatal("quantified SView-returning helper call must decline until its result is binder-parameterized")
	}
	for _, aux := range tr.auxDecls {
		if smtTermMentionsQuantifierBinder(aux) {
			t.Fatalf("quantifier binder escaped into a top-level call auxiliary: %s", aux)
		}
	}

	quantified := analyzeContractStrict(t, "quantified_indexed_sview_call_unproven.elisa", sviewRecordElementTypes+`def same_view(view: sview) -> sview:
    ensure result == view
    return view

def all_named(items: darray[Instruction]) -> bool:
    ensure result == true
    return forall index in 0..<items.count: same_view(items[index].metadata.text) == "label"
`)
	diagnostics := strings.Join(quantified.Errors(), "\n")
	if ensureFailuresFor(quantified.Errors(), "all_named") == 0 {
		t.Fatalf("unsupported quantified call must remain unproven:\n%s", allDiagnostics(quantified))
	}
	if strings.Contains(diagnostics, "unknown constant") || strings.Contains(diagnostics, "SMT parse") {
		t.Fatalf("quantified call must decline cleanly without malformed SMT:\n%s", diagnostics)
	}
}
