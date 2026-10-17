package main

import (
	"elisacore/src/semantic"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func parseInterfaceTestSource(t *testing.T, source string) *ast.File {
	t.Helper()
	return parseInterfaceTestNamedSource(t, "interface.elisa", source)
}

func parseInterfaceTestNamedSource(t *testing.T, filename, source string) *ast.File {
	t.Helper()
	lex := lexer.New(filename, []byte(source))
	tokens := lex.Tokenize()
	if errs := lex.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	parse := parser.New(tokens)
	file := parse.ParseFile(filename)
	if errs := parse.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	return file
}

func TestInterfaceDropsAllPrivateStaticBranches(t *testing.T) {
	source := `static if POSIX:
	@internal
	def posix_only() -> i64:
		return 1
static elif WINDOWS:
	@internal
	def windows_only() -> i64:
		return 2
static else:
	@internal
	def other_only() -> i64:
		return 3
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	if strings.Contains(generated, "static if") || strings.Contains(generated, "posix_only") || strings.Contains(generated, "windows_only") {
		t.Fatalf("interface retained a conditional with no public declarations:\n%s", generated)
	}
	parseInterfaceTestSource(t, generated)
}

func TestInterfaceKeepsPrivateStaticBranchSyntacticallyValid(t *testing.T) {
	source := `static if POSIX:
	@internal
	def hidden() -> i64:
		return 0
static elif WINDOWS:
	def visible() -> i64:
		return 1
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	if strings.Contains(generated, "hidden") || !strings.Contains(generated, "static assert true") || !strings.Contains(generated, "visible") {
		t.Fatalf("interface did not preserve the private branch boundary:\n%s", generated)
	}
	parseInterfaceTestSource(t, generated)
}

func TestInterfacePreservesBoundaryContracts(t *testing.T) {
	for _, source := range []string{
		`def bounded(x: i64 = 7) -> i64 changes x preserves x:
    requires x > 0
    ensure result > 0
    return x
`,
		`extern bounded(x: i64 = 7) -> i64 requires x > 0 ensure result > 0 changes x preserves x
`,
		`protocol Bounded:
    def bounded(x: i64) -> i64 changes x preserves x
        requires x > 0
        ensure result > 0
`,
		`impl i64:
    def bounded(x: i64) -> i64 changes x preserves x:
        requires x > 0
        ensure result > 0
        return x
`,
		`protocol Bounded:
    def bounded(x: i64) -> i64 changes x preserves x:
        requires x > 0
        ensure result > 0
        return x
`,
	} {
		t.Run(strings.Split(source, "\n")[0], func(t *testing.T) {
			original := parseInterfaceTestSource(t, source)
			generated := generateModuleInterface(original)
			for _, clause := range []string{"requires (x > 0)", "ensure (result > 0)", "changes x", "preserves x"} {
				if !strings.Contains(generated, clause) {
					t.Fatalf("lost %q:\n%s", clause, generated)
				}
			}

			reparsed := parseInterfaceTestSource(t, generated)
			if again := generateModuleInterface(reparsed); again != generated {
				t.Fatalf("unstable interface:\n%s\nthen:\n%s", generated, again)
			}
		})
	}
}

func TestInterfaceBoundaryContractEditsChangeInterface(t *testing.T) {
	source := `def bounded(x: i64 = 7) -> i64:
    requires x > 0
    ensure result > 0
    return x
`
	baseline := generateModuleInterface(parseInterfaceTestSource(t, source))
	for _, edit := range [][2]string{{"x > 0", "x > 1"}, {"result > 0", "result > 1"}, {"= 7", "= 8"}} {
		changed := generateModuleInterface(parseInterfaceTestSource(t, strings.Replace(source, edit[0], edit[1], 1)))
		if changed == baseline {
			t.Fatalf("public edit %v did not change interface", edit)
		}
	}
	privateEdit := generateModuleInterface(parseInterfaceTestSource(t, strings.Replace(source, "return x", "return x + 1", 1)))
	if privateEdit != baseline {
		t.Fatal("private ordinary body edit changed declared boundary")
	}
}

func TestInterfaceBoundarySemanticFrameParity(t *testing.T) {
	source := `struct Render:
    px: mutable i32
    health: mutable i32

def bump(s: mutable Render&) changes s.px preserves s.health:
    s.px <- s.px + 1
`
	original := semantic.Analyze(parseInterfaceTestSource(t, source))
	generated := generateModuleInterface(original.File)
	imported := semantic.Analyze(parseInterfaceTestNamedSource(t, "interface.elisai", generated))
	if len(original.Errors()) != 0 || len(imported.Errors()) != 0 {
		t.Fatalf("source %v; interface %v", original.Errors(), imported.Errors())
	}
	before := original.GlobalScope.Symbols["bump"].Type.(*semantic.FuncType)
	after := imported.GlobalScope.Symbols["bump"].Type.(*semantic.FuncType)
	if !before.FrameBounded || !after.FrameBounded || !reflect.DeepEqual(before.FrameWrites, after.FrameWrites) {
		t.Fatalf("frame lost: before %#v/%v after %#v/%v", before.FrameWrites, before.FrameBounded, after.FrameWrites, after.FrameBounded)
	}
	for _, field := range []string{"px", "health"} {
		caller := "\ndef outer(r: mutable Render&) changes r." + field + ":\n    bump(r)\n"
		for _, boundary := range []string{source, generated} {
			filename := "interface.elisa"
			if boundary == generated {
				filename = "interface.elisai"
			}
			result := semantic.Analyze(parseInterfaceTestNamedSource(t, filename, boundary+caller))
			joined := strings.Join(result.Errors(), "\n")
			if field == "px" && len(result.Errors()) != 0 {
				t.Fatalf("disjoint frame rejected: %s", joined)
			}
			if field == "health" && !strings.Contains(joined, "outside the `changes` set") {
				t.Fatalf("overlapping write accepted: %s", joined)
			}
		}
	}
}

func TestInterfaceBoundarySemanticPreconditionParity(t *testing.T) {
	source := `def positive(x: i64 = 7) -> i64:
    requires x > 0
    ensure result > 0
    return x
`
	checked := semantic.AnalyzeWithOptions(parseInterfaceTestSource(t, source), semantic.AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if len(checked.Errors()) != 0 {
		t.Fatal(checked.Errors())
	}
	generated := generateModuleInterface(checked.File)
	for _, arg := range []string{"", "1", "0"} {
		for _, boundary := range []string{source, generated} {
			filename := "interface.elisa"
			if boundary == generated {
				filename = "interface.elisai"
			}
			result := semantic.AnalyzeWithOptions(parseInterfaceTestNamedSource(t, filename, boundary+"\ndef caller() -> i64:\n    return positive("+arg+")\n"), semantic.AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
			if arg == "0" && len(result.Errors()) == 0 {
				t.Fatal("invalid argument accepted")
			}
			if arg != "0" && len(result.Errors()) != 0 {
				t.Fatalf("valid argument %q rejected: %v", arg, result.Errors())
			}
		}
	}
}

func TestInterfacePreservesVariadicBoundary(t *testing.T) {
	source := `def variadic(first: i64, ...) -> i64:
    return first
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	boundary := parseInterfaceTestNamedSource(t, "interface.elisai", generated).Decls[0].(*ast.ExternFuncDecl)
	if !boundary.Variadic {
		t.Fatalf("lost variadic ABI: %s", generated)
	}
}

func TestInterfaceNamedContractsRemainReusableWithoutExpansionGrowth(t *testing.T) {
	source := `law Positive(x: i64) = x > 0

contract PositiveResult(x: i64):
    includes Positive(x)
    ensure result > 0

def positive(x: i64) -> i64:
    uses PositiveResult(x)
    return x

extern native_positive(x: i64) -> i64 uses PositiveResult(x)
`
	options := semantic.AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	checked := semantic.AnalyzeWithOptions(parseInterfaceTestSource(t, source), options)
	if len(checked.Errors()) != 0 {
		t.Fatal(checked.Errors())
	}
	for _, name := range []string{"positive", "native_positive"} {
		ft := checked.GlobalScope.Symbols[name].Type.(*semantic.FuncType)
		if ft.SpecSignature == nil || len(ft.SpecSignature.Requires) != 1 || len(ft.SpecSignature.Ensures) != 1 {
			t.Fatalf("stale canonical source contracts: %#v", ft.SpecSignature)
		}
	}
	generated := generateModuleInterface(checked.File)
	for _, want := range []string{"law Positive(", "contract PositiveResult(", "includes Positive(x)"} {
		if !strings.Contains(generated, want) {
			t.Fatalf("lost %q:\n%s", want, generated)
		}
	}
	if strings.Contains(generated, "uses PositiveResult") {
		t.Fatalf("expanded function application exported twice:\n%s", generated)
	}
	for revision := 0; revision < 3; revision++ {
		imported := semantic.AnalyzeWithOptions(parseInterfaceTestNamedSource(t, "interface.elisai", generated), options)
		if len(imported.Errors()) != 0 {
			t.Fatal(imported.Errors())
		}
		again := generateModuleInterface(imported.File)
		if again != generated {
			t.Fatalf("interface grew at revision %d:\n%s\nthen:\n%s", revision, generated, again)
		}
		generated = again
	}
	for _, call := range []string{"positive(1)", "positive(0)", "native_positive(1)", "native_positive(0)", "consumer(1)", "consumer(0)"} {
		client := `def consumer(x: i64) -> i64:
    uses PositiveResult(x)
    return x

def caller() -> i64:
    return ` + call + "\n"
		result := semantic.AnalyzeWithOptions(parseInterfaceTestNamedSource(t, "interface.elisai", generated+"\n"+client), options)
		if strings.HasSuffix(call, "(0)") && len(result.Errors()) == 0 {
			t.Fatalf("violating exported contract accepted: %s", call)
		}
		if strings.HasSuffix(call, "(1)") && len(result.Errors()) != 0 {
			t.Fatalf("valid exported contract rejected: %s: %v", call, result.Errors())
		}
	}
}

func TestInterfaceNamedFrameContractParity(t *testing.T) {
	source := `struct Render:
    px: mutable i32
    health: mutable i32

law PixelOnly(s: mutable Render&) changes s.px preserves s.health

contract WritePixel(s: mutable Render&):
    includes PixelOnly(s)

def bump(s: mutable Render&):
    uses WritePixel(s)
    s.px <- s.px + 1

extern native_bump(s: mutable Render&) uses WritePixel(s)
`
	checked := semantic.Analyze(parseInterfaceTestSource(t, source))
	if len(checked.Errors()) != 0 {
		t.Fatal(checked.Errors())
	}
	generated := generateModuleInterface(checked.File)
	imported := semantic.Analyze(parseInterfaceTestNamedSource(t, "interface.elisai", generated))
	if len(imported.Errors()) != 0 {
		t.Fatal(imported.Errors())
	}
	if again := generateModuleInterface(imported.File); again != generated {
		t.Fatalf("frame interface grew:\n%s\nthen:\n%s", generated, again)
	}
	for _, name := range []string{"bump", "native_bump"} {
		before := checked.GlobalScope.Symbols[name].Type.(*semantic.FuncType)
		after := imported.GlobalScope.Symbols[name].Type.(*semantic.FuncType)
		if !before.FrameBounded || !after.FrameBounded || !reflect.DeepEqual(before.FrameWrites, after.FrameWrites) {
			t.Fatalf("lost %s frame: %#v / %#v", name, before.FrameWrites, after.FrameWrites)
		}
		for _, field := range []string{"px", "health"} {
			caller := "\ndef caller(r: mutable Render&) changes r." + field + ":\n    " + name + "(r)\n"
			result := semantic.Analyze(parseInterfaceTestNamedSource(t, "interface.elisai", generated+caller))
			if field == "px" && len(result.Errors()) != 0 {
				t.Fatalf("valid caller rejected: %v", result.Errors())
			}
			if field == "health" && !strings.Contains(strings.Join(result.Errors(), "\n"), "outside the `changes` set") {
				t.Fatalf("invalid caller accepted: %v", result.Errors())
			}
		}
	}
}

func TestInterfacePreservesLawKinds(t *testing.T) {
	source := `law Positive(x: i64) = x > 0
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law Leaf includes NoAlloc, NoPanic
law FieldOnly(s: mutable Render&) changes s.px preserves s.health
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	parsed := parseInterfaceTestNamedSource(t, "interface.elisai", generated)
	for _, decl := range parsed.Decls {
		if !decl.(*ast.FuncDecl).IsLaw {
			t.Fatalf("lost law kind: %s", generated)
		}
	}
	if again := generateModuleInterface(parsed); again != generated {
		t.Fatalf("unstable laws:\n%s\nthen:\n%s", generated, again)
	}
}

func TestInterfaceNamedGenericContractRetainsProofAttachments(t *testing.T) {
	source := `contract Ordered[T](lo: T, hi: T):
    requires lo <= hi by scoped:
        assert true
    ensure result >= lo by scoped:
        assert true
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	parsed := parseInterfaceTestNamedSource(t, "interface.elisai", generated)
	contract := parsed.Decls[0].(*ast.FuncDecl)
	if !contract.IsContract || len(contract.GenericParams) != 1 || len(contract.RequiresProofs) != 1 || contract.RequiresProofs[0] == nil || len(contract.RequiresProofs[0].Proof) != 1 || len(contract.EnsureProofs) != 1 || contract.EnsureProofs[0] == nil || len(contract.EnsureProofs[0].Proof) != 1 {
		t.Fatalf("lost generic contract or proof attachment: %#v\n%s", contract, generated)
	}
	if again := generateModuleInterface(parsed); again != generated {
		t.Fatalf("proof contract unstable:\n%s\nthen:\n%s", generated, again)
	}
}

func TestInterfaceProtocolLawRetainsKind(t *testing.T) {
	source := `protocol PositiveValue:
    law Positive(x: i64) = x > 0
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	parsed := parseInterfaceTestNamedSource(t, "interface.elisai", generated)
	law := parsed.Decls[0].(*ast.InterfaceDecl).Members[0].(*ast.FuncDecl)
	if !law.IsLaw {
		t.Fatalf("lost protocol law kind:\n%s", generated)
	}
	if again := generateModuleInterface(parsed); again != generated {
		t.Fatalf("unstable protocol law:\n%s\nthen:\n%s", generated, again)
	}
}
