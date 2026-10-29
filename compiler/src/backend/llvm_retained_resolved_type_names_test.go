//go:build cgo

package backend

import (
	"os"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func parseRetainedTypeNameFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	const filename = "retained-type-name.elisa"
	lex := lexer.New(filename, []byte(source))
	tokens := lex.Tokenize()
	if errors := lex.Errors(); len(errors) != 0 {
		t.Fatalf("lex errors: %v", errors)
	}
	parser := parser.New(tokens)
	file := parser.ParseFile(filename)
	if errors := parser.Errors(); len(errors) != 0 {
		t.Fatalf("parse errors: %v", errors)
	}
	return file
}

func TestRetainedResolvedTypeNameBodyEditMatchesColdLLVMIRAfterRestore(t *testing.T) {
	const baseline = `module Sample:
    public:
        struct Box:
            value: i32

        def changed() -> i32:
            return 1

def main() -> i32:
    return Sample::changed()
`
	const edited = `module Sample:
    public:
        struct Box:
            value: i32

        def changed() -> i32:
            local: Box = Box{value: 7}
            return local.value

def main() -> i32:
    return Sample::changed()
`
	options := semantic.AnalyzeOptions{}
	_, retained := semantic.AnalyzeRetained(parseRetainedTypeNameFixture(t, baseline), parseRetainedTypeNameFixture(t, baseline), []byte(baseline), options)
	if retained == nil {
		t.Fatal("baseline analysis was not retained")
	}
	checkpoint, err := retained.MarshalState()
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	retained, err = semantic.RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatalf("restore baseline: %v", err)
	}
	updated, err := retained.Update(parseRetainedTypeNameFixture(t, edited), parseRetainedTypeNameFixture(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("body-local type annotation update fell back: %v", err)
	}
	warmIR, err := GenerateLLVMIRWithOpt(updated, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate retained LLVM IR: %v", err)
	}
	cold := semantic.AnalyzeWithOptions(parseRetainedTypeNameFixture(t, edited), options)
	coldIR, err := GenerateLLVMIRWithOpt(cold, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate cold LLVM IR: %v", err)
	}
	if warmIR != coldIR {
		t.Fatalf("restored retained LLVM IR differs from cold output\nretained:\n%s\ncold:\n%s", warmIR, coldIR)
	}
}

func TestRetainedFastPreparedTypeNameBodyEditMatchesColdLLVMIR(t *testing.T) {
	if os.Getenv("ELISACORE_RETAINED_FAST_PREPARE") != "on" {
		t.Skip("run with ELISACORE_RETAINED_FAST_PREPARE=on")
	}
	const typed = `module Sample:
    public:
        struct Box:
            value: i32

        def changed() -> i32:
            local: Box = Box{value: 7}
            return local.value

def main() -> i32:
    return Sample::changed()
`
	options := semantic.AnalyzeOptions{}
	_, retained := semantic.AnalyzeRetained(parseRetainedTypeNameFixture(t, typed), parseRetainedTypeNameFixture(t, typed), []byte(typed), options)
	if retained == nil {
		t.Fatal("typed analysis was not retained")
	}
	checkpoint, err := retained.MarshalState()
	if err != nil {
		t.Fatalf("marshal typed baseline: %v", err)
	}
	retained, err = semantic.RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatalf("restore typed body: %v", err)
	}
	for _, value := range []string{"8", "11"} {
		source := strings.Replace(typed, "value: 7", "value: "+value, 1)
		updated, updateErr := retained.Update(parseRetainedTypeNameFixture(t, source), parseRetainedTypeNameFixture(t, source), []byte(source))
		if updateErr != nil {
			t.Fatalf("numeric-only typed edit %s fell back: %v", value, updateErr)
		}
		if !retained.LastStats.FastPrepared {
			t.Fatalf("numeric-only typed edit %s did not use fast preparation: %+v", value, retained.LastStats)
		}
		warmIR, irErr := GenerateLLVMIRWithOpt(updated, OptimizationLevel0)
		if irErr != nil {
			t.Fatalf("generate retained LLVM IR: %v", irErr)
		}
		cold := semantic.AnalyzeWithOptions(parseRetainedTypeNameFixture(t, source), options)
		coldIR, irErr := GenerateLLVMIRWithOpt(cold, OptimizationLevel0)
		if irErr != nil {
			t.Fatalf("generate cold LLVM IR: %v", irErr)
		}
		if warmIR != coldIR {
			t.Fatalf("fast-prepared restored LLVM IR differs for value %s\nretained:\n%s\ncold:\n%s", value, warmIR, coldIR)
		}
		checkpoint, err = retained.MarshalState()
		if err != nil {
			t.Fatalf("marshal after numeric-only edit %s: %v", value, err)
		}
		retained, err = semantic.RestoreAnalysisState(checkpoint)
		if err != nil {
			t.Fatalf("restore after numeric-only edit %s: %v", value, err)
		}
	}
}

// A fast-prepared edit whose body type syntax moved to another structural path
// or uses a spelling only the function header declares must still replay to the
// same declarations and LLVM IR as a cold analysis, including across a
// marshal/restore boundary.
func TestRetainedFastPreparedPathShiftAndAliasEditsMatchColdLLVMIR(t *testing.T) {
	if os.Getenv("ELISACORE_RETAINED_FAST_PREPARE") != "on" {
		t.Skip("run with ELISACORE_RETAINED_FAST_PREPARE=on")
	}
	const baseline = `module Sample:
    public:
        type U8Alias = u8

        struct Box:
            value: i32

        def changed(box: Box) -> i32:
            tiny: Box = Box{value: 7}
            much_longer_local: mutable Box = Box{value: 3}
            return tiny.value + much_longer_local.value + box.value

        def other(byte: U8Alias) -> U8Alias:
            local: u8 = byte
            return local

def main() -> i32:
    return Sample::changed(Sample::Box{value: 2})
`
	shifted := strings.Replace(baseline, `            tiny: Box = Box{value: 7}
            much_longer_local: mutable Box = Box{value: 3}`, `            much_longer_local: Box = Box{value: 7}
            tiny: mutable Box = Box{value: 3}`, 1)
	aliased := strings.Replace(shifted, "            local: u8 = byte", "            local: U8Alias = byte", 1)
	if shifted == baseline || aliased == shifted {
		t.Fatal("fixture edits did not apply")
	}

	options := semantic.AnalyzeOptions{}
	_, retained := semantic.AnalyzeRetained(parseRetainedTypeNameFixture(t, baseline), parseRetainedTypeNameFixture(t, baseline), []byte(baseline), options)
	if retained == nil {
		t.Fatal("baseline analysis was not retained")
	}
	checkpoint, err := retained.MarshalState()
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	retained, err = semantic.RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatalf("restore baseline: %v", err)
	}

	for index, source := range []string{shifted, aliased} {
		updated, updateErr := retained.Update(parseRetainedTypeNameFixture(t, source), parseRetainedTypeNameFixture(t, source), []byte(source))
		if updateErr != nil {
			t.Fatalf("edit %d fell back: %v", index, updateErr)
		}
		if !retained.LastStats.FastPrepared {
			t.Fatalf("edit %d did not use fast preparation: %+v", index, retained.LastStats)
		}
		warmIR, irErr := GenerateLLVMIRWithOpt(updated, OptimizationLevel0)
		if irErr != nil {
			t.Fatalf("generate retained LLVM IR after edit %d: %v", index, irErr)
		}
		cold := semantic.AnalyzeWithOptions(parseRetainedTypeNameFixture(t, source), options)
		coldIR, irErr := GenerateLLVMIRWithOpt(cold, OptimizationLevel0)
		if irErr != nil {
			t.Fatalf("generate cold LLVM IR after edit %d: %v", index, irErr)
		}
		if warmIR != coldIR {
			t.Fatalf("fast-prepared restored LLVM IR differs after edit %d\nretained:\n%s\ncold:\n%s", index, warmIR, coldIR)
		}
		checkpoint, err = retained.MarshalState()
		if err != nil {
			t.Fatalf("marshal after edit %d: %v", index, err)
		}
		retained, err = semantic.RestoreAnalysisState(checkpoint)
		if err != nil {
			t.Fatalf("restore after edit %d: %v", index, err)
		}
	}
}
