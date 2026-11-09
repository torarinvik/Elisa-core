//go:build cgo

package backend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Keep the highest-volume semantic maps behind the explicit per-generator
// read view. This source gate is intentionally structural: adding a new direct
// field read must be routed through the view before the audit can grow.
func TestCodegenGlobalSemanticReadsUseReadView(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		check := func(node ast.Node, function string) {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return
			}
			if selector.Sel.Name == "NamedTypes" || selector.Sel.Name == "GlobalScope" {
				allowedViewMethods := map[string]bool{
					"LookupNamedType": true, "HasNamedTypes": true, "NamedTypesMap": true,
					"HasGlobalScope": true, "LookupGlobalSymbol": true, "GlobalSymbolsMap": true,
				}
				if name != "llvm_codegen_read_view.go" || !allowedViewMethods[function] {
					t.Errorf("%s: direct Result.%s access in %s bypasses the read view", name, selector.Sel.Name, function)
				}
			}
		}
		if name != "llvm_codegen_read_view.go" {
			ast.Inspect(file, func(node ast.Node) bool {
				check(node, "<package>")
				return true
			})
			continue
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Body != nil {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					check(node, function.Name.Name)
					return true
				})
				continue
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				check(node, "<package>")
				return true
			})
		}
	}
}

func TestCodegenASTSemanticReadsUseExplicitReadView(t *testing.T) {
	_, sourceFile, _, _ := runtime.Caller(0)
	backendDir := filepath.Dir(sourceFile)
	generated, err := os.ReadFile(filepath.Join(backendDir, "..", "semantic", "codegen_read_view_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	tableMatches := regexp.MustCompile(`Table: "([A-Za-z0-9_]+)"`).FindAllSubmatch(generated, -1)
	tables := make(map[string]bool, len(tableMatches))
	for _, match := range tableMatches {
		tables[string(match[1])] = true
	}
	if len(tables) != 34 {
		t.Fatalf("generated AST-fact read schema has %d tables, want 34; review and extend the source gate", len(tables))
	}
	getterNames := make(map[string]bool, len(tables)*4)
	for table := range tables {
		getterNames["Lookup"+table] = true
		getterNames["Value"+table] = true
		getterNames["Range"+table] = true
		getterNames[table+"Len"] = true
	}
	for _, name := range []string{
		"ExprOptimizationFacts",
		"ExprsHaveSameExtent",
		"ExprsHaveEqualExtentSize",
		"ExprsAreDisjoint",
		"ExprSupportsDenseWrite",
		"ExprPackedStoreProvenance",
		"ExprHasPackedStoreProvenance",
		"ExprDependsOnFrozenPackedStores",
		"ExprDependsOnNonFrozenPackedStores",
		"ExprHasOnlyFrozenPackedStoreDeps",
		"ExprHasMixedPackedStoreProvenance",
		"ExprDependsOnlyOnFrozenPackedStores",
	} {
		getterNames[name] = true
	}

	files, err := filepath.Glob(filepath.Join(backendDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	parsed := make(map[string]*ast.File)
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed[name] = file
	}
	for _, diagnostic := range codegenASTReadDiagnostics(parsed, tables, getterNames, true) {
		t.Error(diagnostic)
	}
}

func TestCodegenASTReadGateAcceptsQualifiedViewAndRejectsSpoofs(t *testing.T) {
	getterNames := map[string]bool{"LookupExprTypes": true}
	tables := map[string]bool{"ExprTypes": true}
	good := parseCodegenGateFixture(t, "llvm_codegen_read_view.go", `package backend
import sem "elisacore/src/semantic"
type llvmGenerator struct{}
func (g *llvmGenerator) semanticReadView() sem.CodegenReadView { return sem.CodegenReadView{} }
func (g *llvmGenerator) semanticReadViewFor(r *sem.Result) sem.CodegenReadView { return sem.CodegenReadView{} }
func useView(v sem.CodegenReadView, g *llvmGenerator) {
    _ = v.LookupExprTypes(nil)
    _, _ = v.ExprOptimizationFacts(nil)
    _ = g.semanticReadView().LookupExprTypes(nil)
}`)
	for _, name := range []string{
		"ExprOptimizationFacts", "ExprsHaveSameExtent", "ExprsHaveEqualExtentSize", "ExprsAreDisjoint",
		"ExprSupportsDenseWrite", "ExprPackedStoreProvenance", "ExprHasPackedStoreProvenance",
		"ExprDependsOnFrozenPackedStores", "ExprDependsOnNonFrozenPackedStores",
		"ExprHasOnlyFrozenPackedStoreDeps", "ExprHasMixedPackedStoreProvenance",
		"ExprDependsOnlyOnFrozenPackedStores",
	} {
		getterNames[name] = true
	}
	if diagnostics := codegenASTReadDiagnostics(map[string]*ast.File{"llvm_codegen_read_view.go": good}, tables, getterNames, false); len(diagnostics) != 0 {
		t.Fatalf("valid qualified view fixture rejected: %v", diagnostics)
	}

	bad := parseCodegenGateFixture(t, "spoofed.go", `package backend
import sem "elisacore/src/semantic"
import wrong "example.invalid/semantic"
var _ = func(g *llvmGenerator) { _ = g.ExprTypes[n] }
type CodegenReadView = int
type fake struct{}
func (f *fake) semanticReadView() sem.CodegenReadView { return sem.CodegenReadView{} }
type holder struct { semanticReadView func() sem.CodegenReadView }
func wrongPackage(v wrong.CodegenReadView) { _ = v.LookupExprTypes(nil) }
func fakeAlias(v CodegenReadView) { _ = v.LookupExprTypes(nil) }
func fakeFactory(f *fake) { _ = f.semanticReadView().LookupExprTypes(nil) }
func fakeField(h holder) { _ = h.semanticReadView().LookupExprTypes(nil) }
func directResultFacts(r *sem.Result) { _ = r.ExprsAreDisjoint(nil, nil) }`)
	diagnostics := codegenASTReadDiagnostics(map[string]*ast.File{
		"llvm_codegen_read_view.go": good,
		"spoofed.go":                bad,
	}, tables, getterNames, false)
	joined := strings.Join(diagnostics, "\n")
	for _, expected := range []string{
		"direct semantic Result.ExprTypes read in <package>",
		"LookupExprTypes call in wrongPackage bypasses",
		"LookupExprTypes call in fakeAlias bypasses",
		"reserved read-view factory semanticReadView is not an approved llvmGenerator bridge method",
		"LookupExprTypes call in fakeFactory bypasses",
		"reserved read-view factory name semanticReadView declared as a struct or interface field",
		"LookupExprTypes call in fakeField bypasses",
		"ExprsAreDisjoint call in directResultFacts bypasses",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("source gate did not reject fixture for %q; diagnostics:\n%s", expected, joined)
		}
	}
}

func parseCodegenGateFixture(t *testing.T, filename, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", filename, err)
	}
	return file
}

func codegenASTReadDiagnostics(files map[string]*ast.File, tables, getterNames map[string]bool, checkWriteSites bool) []string {
	var diagnostics []string
	factories, factoryDiagnostics := codegenReadViewFactories(files)
	diagnostics = append(diagnostics, factoryDiagnostics...)
	structFields := codegenStructFields(files)
	seenWrites := map[string]int{}
	checkNode := func(filename, function string, node ast.Node, viewParams map[string]bool, functionDecl *ast.FuncDecl) {
		allowedWrites := map[*ast.SelectorExpr]bool{}
		if filename == "llvm_partition_build.go" && functionDecl != nil {
			switch functionDecl.Name.Name {
			case "setResultExprType":
				ast.Inspect(functionDecl.Body, func(node ast.Node) bool {
					assignment, ok := node.(*ast.AssignStmt)
					if !ok || assignment.Tok != token.ASSIGN {
						return true
					}
					for _, lhs := range assignment.Lhs {
						index, ok := lhs.(*ast.IndexExpr)
						if !ok {
							continue
						}
						selector, ok := index.X.(*ast.SelectorExpr)
						if ok && selector.Sel.Name == "ExprTypes" && isGeneratorResultField(selector) {
							allowedWrites[selector] = true
							seenWrites["setResultExprType"]++
						}
					}
					return true
				})
			case "deleteResultExprType":
				ast.Inspect(functionDecl.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) != 2 {
						return true
					}
					callee, ok := call.Fun.(*ast.Ident)
					if !ok || callee.Name != "delete" {
						return true
					}
					selector, ok := call.Args[0].(*ast.SelectorExpr)
					if ok && selector.Sel.Name == "ExprTypes" && isGeneratorResultField(selector) {
						allowedWrites[selector] = true
						seenWrites["deleteResultExprType"]++
					}
					return true
				})
			}
		}
		ast.Inspect(node, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if tables[selector.Sel.Name] && !allowedWrites[selector] {
				diagnostics = append(diagnostics, filename+": direct semantic Result."+selector.Sel.Name+" read in "+function+" bypasses CodegenReadView; use a logical read or physical Own/HasOwn method")
			}
			if getterNames[selector.Sel.Name] && !isExplicitCodegenReadView(selector.X, viewParams, functionDecl, structFields, factories) {
				diagnostics = append(diagnostics, filename+": "+selector.Sel.Name+" call in "+function+" bypasses the generator-local semantic read view")
			}
			return true
		})
	}
	for filename, file := range files {
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Body != nil {
				checkNode(filename, function.Name.Name, function.Body, codegenReadViewParameters(function, file), function)
				continue
			}
			// Package-level initializers and type declarations have no trusted
			// generator/view parameters. Scan them anyway so a getter or raw map
			// read hidden in a function literal cannot evade this source gate.
			checkNode(filename, "<package>", declaration, nil, nil)
		}
	}
	if checkWriteSites && (seenWrites["setResultExprType"] != 1 || seenWrites["deleteResultExprType"] != 1) {
		diagnostics = append(diagnostics, "expected the two reviewed ExprTypes write sites to remain exact; got set="+strconv.Itoa(seenWrites["setResultExprType"])+" delete="+strconv.Itoa(seenWrites["deleteResultExprType"]))
	}
	return diagnostics
}
