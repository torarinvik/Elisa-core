package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"elisacore/src/ast"
)

func portableBodyFixture() (*ast.File, *ast.FuncDecl, *ast.FuncDecl) {
	first := &ast.FuncDecl{Name: "first"}
	second := &ast.FuncDecl{Name: "work"}
	file := &ast.File{Filename: "src/example.elisa", Decls: []ast.Decl{first, &ast.NamespaceDecl{Name: "N", Decls: []ast.Decl{second}}}}
	return file, first, second
}

func TestPortableBodyObservationsStableAndFailClosed(t *testing.T) {
	file, first, second := portableBodyFixture()
	r := &irInputReadRecorder{}
	r.body(second, first, "specialized-body")
	r.body(first, nil, "owned-body")
	report := r.snapshot()
	if len(report.Uncovered) == 0 {
		t.Fatal("portable identities must not grant reuse authority")
	}
	got, err := report.portableBodyObservations(file)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	other, otherFirst, otherSecond := portableBodyFixture()
	otherRecorder := &irInputReadRecorder{}
	otherRecorder.body(otherSecond, otherFirst, "specialized-body")
	otherRecorder.body(otherFirst, nil, "owned-body")
	otherRows, err := otherRecorder.snapshot().portableBodyObservations(other)
	if err != nil || !reflect.DeepEqual(got, otherRows) {
		t.Fatalf("equivalent fresh graph changed locators: %s, %v", encoded, err)
	}
	other.Filename = "different.elisa"
	rows, err := otherRecorder.snapshot().portableBodyObservations(other)
	if err != nil || reflect.DeepEqual(got, rows) {
		t.Fatalf("source root identity omitted: %v", err)
	}
	foreign := &ast.FuncDecl{Name: second.Name}
	if _, err := (irInputReadReport{Bodies: []irFunctionBodyRead{{Declaration: foreign}}}).portableBodyObservations(file); err == nil {
		t.Fatal("accepted a same-name foreign declaration")
	}
	if _, err := (irInputReadReport{Bodies: []irFunctionBodyRead{{Declaration: foreign, Canonical: second}}}).portableBodyObservations(file); err == nil {
		t.Fatal("accepted an unauthenticated canonical claim")
	}
	if _, err := (irInputReadReport{Bodies: []irFunctionBodyRead{{Declaration: first, Canonical: foreign}}}).portableBodyObservations(file); err == nil {
		t.Fatal("accepted a foreign canonical declaration")
	}
	// Different private bodies may share a template. Their observed syntax
	// must differ, while the semantic facts remain unverified.
	clone1 := &ast.FuncDecl{Name: second.Name, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}}
	clone2 := &ast.FuncDecl{Name: second.Name, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "2"}}}}
	private := &irInputReadRecorder{}
	private.bodyMapped(clone1, second, "specialized-body")
	private.bodyMapped(clone2, second, "specialized-body")
	privateReport := private.snapshot()
	rows, err = privateReport.portableBodyObservations(file)
	if err != nil || len(rows) != 2 || !rows[0].PrivateBodyUnverified || !rows[1].PrivateBodyUnverified || rows[0].Declaration != nil || rows[0].SyntaxDigest == rows[1].SyntaxDigest || len(privateReport.Uncovered) == 0 {
		t.Fatalf("private bodies must remain unverified despite shared source identity: %#v, %v", rows, err)
	}
	// A body can be observed twice around a rewrite. The trace must retain both
	// observed versions, not silently deduplicate by process-local pointer.
	mutations := &irInputReadRecorder{}
	mutations.bodyMapped(clone1, second, "specialized-body")
	clone1.Body = clone2.Body
	mutations.bodyMapped(clone1, second, "specialized-body")
	observed := mutations.snapshot()
	versions, err := observed.portableBodyObservations(file)
	if err != nil || len(versions) != 2 || versions[0].SyntaxDigest == versions[1].SyntaxDigest {
		t.Fatalf("mutation lost an observed syntax version: %#v, %v", versions, err)
	}
	// Do not turn an unsupported or missing digest into a portable identity.
	if _, err := (irInputReadReport{Bodies: []irFunctionBodyRead{{Declaration: first}}}).portableBodyObservations(file); err == nil {
		t.Fatal("accepted observation without syntax digest")
	}
	file.Decls = append(file.Decls, first)
	if _, err := report.portableBodyObservations(file); err == nil {
		t.Fatal("accepted ambiguous aliased source declarations")
	}
	if _, err := report.portableBodyObservations(&ast.File{}); err == nil {
		t.Fatal("accepted missing source root")
	}
}

func TestPortableBodyObservationsRealGenericCodegen(t *testing.T) {
	const source = `def keep[T](value: T) -> T:
    return value

def main() -> i64:
    return keep(23)
`
	result := parseAndAnalyzeBackendTest(t, "portable-body-generic.elisa", source)
	recorder := &irInputReadRecorder{}
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { g.inputReads = recorder })
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	report := recorder.snapshot()
	if len(report.Bodies) == 0 {
		t.Fatal("generic lowering produced no body observations")
	}
	private := 0
	for _, body := range report.Bodies {
		rows, err := (irInputReadReport{Bodies: []irFunctionBodyRead{body}}).portableBodyObservations(result.ActiveFile())
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].PrivateBodyUnverified {
			private++
		}
	}
	if private == 0 || len(report.Uncovered) == 0 {
		t.Fatal("private generic bodies or remaining coverage gaps were not marked unverified")
	}
}

// Compare reports from independent compiler processes. Unresolvable private
// copies are recorded explicitly, never attributed to a source declaration.
func TestPortableBodyObservationsRealPartitionsFreshProcesses(t *testing.T) {
	if os.Getenv("ELISA_PORTABLE_BODY_CHILD") == "1" {
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		const source = `def keep[T](value: T) -> T:
    return value

def first() -> i64:
    return keep(23)

def second() -> i64:
    return keep(24)

def main() -> i64:
    return first() + second()
`
		result := parseAndAnalyzeBackendTest(t, "portable-partitions.elisa", source)
		session := NewIncrementalObjectSession(2, 32<<20)
		session.minWeight = 0
		var mu sync.Mutex
		var summaries []string
		session.irInputAudit = func(part int, report irInputReadReport) {
			for _, body := range report.Bodies {
				rows, err := (irInputReadReport{Bodies: []irFunctionBodyRead{body}}).portableBodyObservations(result.ActiveFile())
				var value string
				if err != nil {
					value = fmt.Sprintf("%d:unresolved:%s", part, body.Use)
				} else {
					data, marshalErr := json.Marshal(rows[0])
					if marshalErr != nil {
						t.Errorf("encode: %v", marshalErr)
						return
					}
					kind := "resolved"
					if rows[0].PrivateBodyUnverified {
						kind = "private-unverified"
					}
					value = fmt.Sprintf("%d:%s:%s", part, kind, data)
				}
				mu.Lock()
				summaries = append(summaries, value)
				mu.Unlock()
			}
		}
		if err := session.WriteObject(result, filepath.Join(t.TempDir(), "out.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		if stats := session.Stats(); stats.PartitionsEmitted != 2 {
			t.Fatalf("partitions not exercised: %+v", stats)
		}
		sort.Strings(summaries)
		if len(summaries) == 0 {
			t.Fatal("no partition body observations")
		}
		resolved, private := 0, 0
		for _, summary := range summaries {
			if strings.Contains(summary, ":resolved:") {
				resolved++
			}
			if strings.Contains(summary, ":private-unverified:") {
				private++
			}
		}
		if resolved == 0 || private == 0 {
			t.Fatalf("expected source bodies and unverified private bodies; got %v", summaries)
		}
		data, err := json.Marshal(summaries)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("ELISA_PORTABLE_BODY_OUTPUT"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	var outputs [][]byte
	for i := 0; i < 2; i++ {
		path := filepath.Join(root, fmt.Sprintf("%d.json", i))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPortableBodyObservationsRealPartitionsFreshProcesses$")
		cmd.Env = append(os.Environ(), "ELISA_PORTABLE_BODY_CHILD=1", "ELISA_PORTABLE_BODY_OUTPUT="+path)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, output)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, data)
	}
	if !reflect.DeepEqual(outputs[0], outputs[1]) {
		t.Fatalf("partition observations differ across processes:\n%s\n%s", outputs[0], outputs[1])
	}
}
