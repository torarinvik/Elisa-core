//go:build cgo

package semantic

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func actualConcurrencyOwnerRewrapSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing test source path")
	}
	bytes, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../runtime/elisacore_std/elisacore_runtime_concurrency.elisa"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(bytes)
	var out strings.Builder
	for _, name := range []string{"ctx_thread_from_raw", "ctx_thread_to_raw", "ctx_task_from_raw", "ctx_task_to_raw"} {
		start := strings.Index(source, "def "+name+"[")
		if start < 0 {
			t.Fatal("missing actual runtime helper", name)
		}
		end := strings.Index(source[start+1:], "\ndef ")
		if end < 0 {
			t.Fatal("missing helper boundary", name)
		}
		out.WriteString(source[start : start+1+end])
		out.WriteString("\n\n")
	}
	return out.String()
}

func TestActualConcurrencyOwnerRewrapSpecializationTransfersWholeOwner(t *testing.T) {
	source := actualConcurrencyOwnerRewrapSource(t)
	root := analyzeTreeTestSource(t, "owner-rewrap.elisa", source)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	for _, test := range []struct{ name, state string }{{"ctx_thread_from_raw", "Joinable"}, {"ctx_thread_to_raw", "Joinable"}, {"ctx_task_from_raw", "Pending"}, {"ctx_task_to_raw", "Pending"}} {
		fn := retainedProofNamedDecl(t, root, test.name).(*ast.FuncDecl)
		if len(fn.Body) != 2 {
			t.Fatal("runtime helper is not an explicit complete-owner destructure + rewrap", test.name)
		}
		if _, ok := fn.Body[0].(*ast.MoveBindStmt); !ok {
			t.Fatal("helper never moves its complete input", test.name)
		}
		product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"], root.NamedTypes[test.state]})
		if err != nil {
			t.Fatal(test.name, err)
		}
		returned := product.Decl.Body[1].(*ast.ReturnStmt).Value
		actual, ok := product.Result.ValueExprTypes(returned).(*GenericInstanceType)
		if !ok || len(actual.Args) != 2 || !SameType(actual.Args[1], root.NamedTypes[test.state]) {
			t.Fatal("rewrap changed protocol state", test.name, actual)
		}
		// Successful private construction must be memoized; no diagnostics are lost.
		again, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"], root.NamedTypes[test.state]})
		if err != nil || again != product {
			t.Fatal("successful ownership proof was not stable/memoized", test.name, err)
		}
	}
	if len(root.Errors()) != 0 {
		t.Fatal("private specialization altered canonical diagnostics", root.Errors())
	}
}

func TestActualConcurrencyOwnerRewrapDoesNotDependOnHelperNames(t *testing.T) {
	source := strings.ReplaceAll(actualConcurrencyOwnerRewrapSource(t), "ctx_", "renamed_")
	root := analyzeTreeTestSource(t, "untrusted-renamed-rewrap.elisa", source)
	for _, test := range []struct{ name, state string }{{"renamed_thread_from_raw", "Joinable"}, {"renamed_thread_to_raw", "Joinable"}, {"renamed_task_from_raw", "Pending"}, {"renamed_task_to_raw", "Pending"}} {
		fn := retainedProofNamedDecl(t, root, test.name).(*ast.FuncDecl)
		if _, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"], root.NamedTypes[test.state]}); err != nil {
			t.Fatal("ordinary move-as semantics depended on runtime helper identity", test.name, err)
		}
	}
}

func TestActualConcurrencyOwnerRewrapRejectsLeakUseAfterAndDuplicateMove(t *testing.T) {
	actual := actualConcurrencyOwnerRewrapSource(t)
	old := strings.Replace(actual, "\tmove task as {handle, state}\n\treturn Task[void&?, S]{handle: handle, state: state}", "\treturn Task[void&?, S]{handle: task.handle, state: task.state}", 1)
	useAfter := strings.Replace(actual, "\tmove task as {handle, state}\n\treturn Task[void&?, S]", "\tmove task as {handle, state}\n\tobserved: uintptr = task.handle\n\treturn Task[void&?, S]", 1)
	duplicate := strings.Replace(actual, "\tmove task as {handle, state}\n\treturn Task[void&?, S]", "\tmove task as {handle, state}\n\tmove task as {handle: duplicate_handle, state: duplicate_state}\n\treturn Task[void&?, S]", 1)
	for _, test := range []struct{ name, source, diagnostic string }{{"old_field_copy", old, "must be consumed before scope exit"}, {"use_after", useAfter, "cannot be used"}, {"duplicate_move", duplicate, "cannot be used"}} {
		t.Run(test.name, func(t *testing.T) {
			if test.source == actual {
				t.Fatal("negative control did not alter actual helper")
			}
			root := analyzeTreeTestSourceWithSemanticErrors(t, "owner-rewrap-negative.elisa", test.source)
			if errors := strings.Join(root.Errors(), "\n"); errors != "" {
				if test.name == "old_field_copy" || !strings.Contains(errors, test.diagnostic) {
					t.Fatal("unexpected template diagnostics", errors)
				}
				return // Use-after/duplicate may be rejected already at template analysis.
			}
			fn := retainedProofNamedDecl(t, root, "ctx_task_to_raw").(*ast.FuncDecl)
			root.BeginSharedCodegen()
			defer root.EndSharedCodegen()
			product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"], root.NamedTypes["Pending"]})
			if product != nil || err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("unsafe helper admitted or wrong reason: product=%v error=%v", product, err)
			}
			if len(root.shared.bodies) != 0 {
				t.Fatal("failed must-consume specialization entered memo")
			}
			if len(root.Errors()) != 0 {
				t.Fatal("private diagnostic escaped into canonical result")
			}
		})
	}
}

func TestActualConcurrencyOwnerRewrapPreservesReturnedObligation(t *testing.T) {
	source := actualConcurrencyOwnerRewrapSource(t) + `def discard(task: Task[i64, Pending]):
    raw: Task[void&?, Pending] = ctx_task_to_raw(move task)
`
	root := analyzeTreeTestSourceWithSemanticErrors(t, "owner-rewrap-discard.elisa", source)
	if errors := strings.Join(root.Errors(), "\n"); !strings.Contains(errors, "pending task handle") || !strings.Contains(errors, "must be consumed before scope exit") {
		t.Fatal("rewrap erased returned must-consume obligation", errors)
	}
	source = actualConcurrencyOwnerRewrapSource(t) + `def forge(task: Task[i64, Pending]) -> Task[void&?, Held]:
    return (move task).cast[Task[void&?, Held]]
`
	root = analyzeTreeTestSourceWithSemanticErrors(t, "owner-rewrap-forge.elisa", source)
	if errors := strings.Join(root.Errors(), "\n"); !strings.Contains(errors, "invalid cast") {
		t.Fatal("unrelated typestate cast became permitted", errors)
	}
}
