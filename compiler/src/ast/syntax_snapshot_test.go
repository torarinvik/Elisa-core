package ast

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestSyntaxSnapshotOwnsGraphAndVisibility(t *testing.T) {
	raw := []byte(strings.Repeat("p", 8192) + "name")
	name := unsafe.String(&raw[8192], 4)
	shared := &IntLit{Value: "21"}
	fn := &FuncDecl{Name: name, Body: []Stmt{&ReturnStmt{Value: &BinaryExpr{Left: shared, Right: shared}}}}
	file := &File{Decls: []Decl{fn}, DeclVisibility: map[Decl]string{fn: "private"}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	fn.Body = nil
	first, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	a := first.Decls[0].(*FuncDecl)
	b := second.Decls[0].(*FuncDecl)
	if a.Name != "name" || a == fn || a == b || first.DeclVisibility[a] != "private" {
		t.Fatal("snapshot retained source graph or visibility key")
	}
	expr := a.Body[0].(*ReturnStmt).Value.(*BinaryExpr)
	if expr.Left != expr.Right || expr.Left == shared {
		t.Fatal("node sharing not preserved independently")
	}
	expr.Left.(*IntLit).Value = "99"
	if b.Body[0].(*ReturnStmt).Value.(*BinaryExpr).Left.(*IntLit).Value != "21" {
		t.Fatal("copies shared mutable nodes")
	}
}

func TestTransientSyntaxSnapshotBorrowsTextButCopiesNodes(t *testing.T) {
	name := strings.Clone("transient-function-name")
	fn := &FuncDecl{Name: name, Body: []Stmt{&ReturnStmt{Value: &IntLit{Value: "21"}}}}
	snapshot, err := NewTransientSyntaxSnapshot(&File{Decls: []Decl{fn}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fn.Body = nil
	file, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	got := file.Decls[0].(*FuncDecl)
	if got == fn || len(got.Body) != 1 || got.Name != name || unsafe.StringData(got.Name) != unsafe.StringData(name) {
		t.Fatal("transient snapshot did not isolate syntax and retain immutable text")
	}
}

func TestTransientSyntaxSnapshotConcurrentCaptures(t *testing.T) {
	file := &File{Decls: []Decl{&FuncDecl{Name: "f", Body: []Stmt{&ReturnStmt{Value: &IntLit{Value: "21"}}}}}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repeat := 0; repeat < 64; repeat++ {
				snapshot, err := NewTransientSyntaxSnapshot(file, 1<<20)
				if err != nil {
					t.Error(err)
					return
				}
				copy, err := snapshot.CopyFile()
				if err != nil || !reflect.DeepEqual(copy, file) {
					t.Errorf("capture changed graph: %v", err)
					return
				}
				copy.Decls[0].(*FuncDecl).Name = "changed"
			}
		}()
	}
	wg.Wait()
}

func BenchmarkTransientSyntaxSnapshot(b *testing.B) {
	args := make([]Expr, 200)
	for i := range args {
		args[i] = &CallExpr{Func: &Ident{Name: "x"}}
	}
	file := &File{Decls: []Decl{&ConstDecl{Value: &CallExpr{Args: args}}}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := NewTransientSyntaxSnapshot(file, 1<<20); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTransientSnapshotIdentifierSlabBudget(t *testing.T) {
	args := make([]Expr, 20)
	for i := range args {
		args[i] = &CallExpr{Func: &Ident{Name: "x"}}
	}
	file := &File{Decls: []Decl{&ConstDecl{Value: &CallExpr{Args: args}}}}
	snapshot, err := NewTransientSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTransientSyntaxSnapshot(file, snapshot.RetainedBytes()); err != nil {
		t.Fatalf("exact retained budget rejected: %v", err)
	}
	if _, err := NewTransientSyntaxSnapshot(file, snapshot.RetainedBytes()-1); err == nil {
		t.Fatal("snapshot exceeded its retained budget")
	}
}

func TestTransientSnapshotFieldSlabBudgetAndIsolation(t *testing.T) {
	args := make([]Expr, 20)
	for i := range args {
		args[i] = &FieldExpr{Object: &Ident{Name: "x"}, Field: "value"}
	}
	file := &File{Decls: []Decl{&ConstDecl{Value: &CallExpr{Args: args}}}}
	snapshot, err := NewTransientSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTransientSyntaxSnapshot(file, snapshot.RetainedBytes()-1); err == nil {
		t.Fatal("snapshot exceeded retained budget")
	}
	copy, err := snapshot.CopyFile()
	if err != nil || !reflect.DeepEqual(copy, file) {
		t.Fatalf("copy differs: %v", err)
	}
	copy.Decls[0].(*ConstDecl).Value.(*CallExpr).Args[0].(*FieldExpr).Field = "changed"
	if args[0].(*FieldExpr).Field != "value" {
		t.Fatal("snapshot shares mutable fields")
	}
}

func TestSyntaxSnapshotCyclesAndContainerAliases(t *testing.T) {
	call := &CallExpr{}
	args := []Expr{call}
	call.Args = args
	call.ResolvedArgs = args
	call.ResolvedCommonArgs = map[string]Expr{"self": call}
	file := &File{Decls: []Decl{&ConstDecl{Name: "cycle", Value: call}}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	got := copy.Decls[0].(*ConstDecl).Value.(*CallExpr)
	if got == call || got.Args[0] != got || got.ResolvedCommonArgs["self"] != got || &got.Args[0] != &got.ResolvedArgs[0] {
		t.Fatal("cycle/alias changed")
	}
}

func TestSyntaxSnapshotPreservesAccessibleSliceTail(t *testing.T) {
	hidden := &IntLit{Value: "42"}
	backing := []Expr{&IntLit{Value: "1"}, hidden}
	call := &CallExpr{Args: backing[:1]}
	file := &File{Decls: []Decl{&ConstDecl{Value: call}}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	got := copy.Decls[0].(*ConstDecl).Value.(*CallExpr).Args
	if cap(got) != 2 || got[:cap(got)][1] == hidden || got[:cap(got)][1].(*IntLit).Value != "42" {
		t.Fatal("capacity-visible syntax lost or shared")
	}
}

func TestSyntaxSnapshotDeclinesOverlappingViewsAndBudgets(t *testing.T) {
	backing := []Expr{&IntLit{Value: "1"}, &IntLit{Value: "2"}}
	call := &CallExpr{Args: backing, ResolvedArgs: backing[:1]}
	if _, err := NewSyntaxSnapshot(&File{Decls: []Decl{&ConstDecl{Value: call}}}, 1<<20); err == nil {
		t.Fatal("partial slice alias silently copied")
	}
	if _, err := NewSyntaxSnapshot(&File{Decls: []Decl{&ConstDecl{Name: "large"}}}, 1); err == nil {
		t.Fatal("byte budget ignored")
	}
	if _, err := NewSyntaxSnapshot(nil, 1<<20); err == nil {
		t.Fatal("missing graph admitted")
	}
}

func TestSyntaxSnapshotCopyConcurrency(t *testing.T) {
	file := &File{Decls: []Decl{&ConstDecl{Name: "x", Value: &IntLit{Value: "1"}}}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repeat := 0; repeat < 32; repeat++ {
				copy, err := snapshot.CopyFile()
				if err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(copy, file) {
					t.Error("copy changed graph")
				}
				copy.Decls[0].(*ConstDecl).Name = "changed"
			}
		}()
	}
	wg.Wait()
}

func TestSyntaxSnapshotGeneratedReplayMatchesReference(t *testing.T) {
	if !FastSyntaxCopiesAvailable() {
		t.Fatal("generated schema is stale")
	}
	call := &CallExpr{}
	args := []Expr{call, &IntLit{Value: "42"}}
	call.Args = args[:1]
	call.ResolvedArgs = args[:1]
	call.ResolvedCommonArgs = map[string]Expr{"self": call}
	fn := &FuncDecl{Name: "fixture", Body: []Stmt{&ReturnStmt{Value: call}}}
	original := &File{Decls: []Decl{fn}, DeclVisibility: map[Decl]string{fn: "private"}}
	snapshot, err := NewSyntaxSnapshot(original, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reference := newSyntaxCopier(false, snapshot.bytes)
	slow := reference.copy(reflect.ValueOf(snapshot.file), 0)
	if reference.err != nil {
		t.Fatal(reference.err)
	}
	fast, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	referenceFile := slow.Interface().(*File)
	// Maps with pointer keys cannot be DeepEqual across private graph copies.
	if !reflect.DeepEqual(fast.Decls, referenceFile.Decls) || fast.Filename != referenceFile.Filename || len(fast.DeclVisibility) != len(referenceFile.DeclVisibility) {
		t.Fatal("generated replay differs from checked reference")
	}
	for i, decl := range fast.Decls {
		if fast.DeclVisibility[decl] != referenceFile.DeclVisibility[referenceFile.Decls[i]] {
			t.Fatal("visibility key rebinding differs")
		}
	}
}

func TestGeneratedSyntaxCaptureMatchesOwnedReflection(t *testing.T) {
	raw := []byte("file:name:value:tail")
	text := unsafe.String(unsafe.SliceData(raw), len(raw))
	names := []string{text[5:9], text[16:]}
	args := []Expr{&IntLit{Value: text[10:15]}, &IntLit{Value: text[16:]}}
	call := &CallExpr{Args: args[:1], ResolvedArgs: args[:1], ArgNames: names[:1]}
	fn := &FuncDecl{Name: text[5:9], TypeParams: names[:1], Body: []Stmt{&ReturnStmt{Value: call}}}
	original := &File{Filename: text[:4], Decls: []Decl{fn}, DeclVisibility: map[Decl]string{fn: text[5:9]}}
	reference := newSyntaxCopier(true, 1<<20)
	reference.charge(int(reflect.TypeOf(SyntaxSnapshot{}).Size()))
	copied := reference.copy(reflect.ValueOf(original), 0)
	reference.checkStorage()
	if reference.err != nil {
		t.Fatal(reference.err)
	}
	snapshot, err := NewSyntaxSnapshot(original, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RetainedBytes() != reference.bytes {
		t.Fatalf("capture accounting diverged: %d / %d", snapshot.RetainedBytes(), reference.bytes)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	got, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	want := copied.Interface().(*File)
	if got.Filename != want.Filename || !reflect.DeepEqual(got.Decls, want.Decls) || got.DeclVisibility[got.Decls[0]] != want.DeclVisibility[want.Decls[0]] {
		t.Fatal("generated capture lost source ownership or syntax")
	}
	gotFn := got.Decls[0].(*FuncDecl)
	gotCall := gotFn.Body[0].(*ReturnStmt).Value.(*CallExpr)
	if gotFn.TypeParams[:2][1] != "tail" || gotCall.ArgNames[:2][1] != "tail" || gotCall.Args[:2][1].(*IntLit).Value != "tail" {
		t.Fatal("capacity-visible strings/nodes retained source bytes")
	}
}

func TestGeneratedSyntaxCaptureDepthBound(t *testing.T) {
	var value Expr = &IntLit{Value: "1"}
	for i := 0; i < 3000; i++ {
		value = &BinaryExpr{Left: value}
	}
	if snapshot, err := NewSyntaxSnapshot(&File{Decls: []Decl{&ConstDecl{Value: value}}}, 8<<20); err == nil || snapshot != nil {
		t.Fatal("unbounded capture depth admitted")
	}
}

// Test-only implementation deliberately absent from the generated schema.
type unsupportedSyntaxExpr struct{ IntLit }

func TestGeneratedSyntaxCaptureRejectsUnknownImplementation(t *testing.T) {
	file := &File{Decls: []Decl{&ConstDecl{Value: &unsupportedSyntaxExpr{}}}}
	if snapshot, err := NewSyntaxSnapshot(file, 1<<20); err == nil || snapshot != nil {
		t.Fatal("unrecognized syntax implementation admitted")
	}
}

func TestGeneratedSyntaxCapturePreservesTypedNil(t *testing.T) {
	var value *IntLit
	file := &File{Decls: []Decl{&ConstDecl{Value: value}}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	got := copied.Decls[0].(*ConstDecl).Value
	if got == nil || !reflect.DeepEqual(got, value) {
		t.Fatal("typed nil syntax changed")
	}
}

func TestSyntaxSnapshotPrefixOwnsNestedVisibilityAndDropsSuffix(t *testing.T) {
	nested := &FuncDecl{Name: "same"}
	suffix := &FuncDecl{Name: "same"}
	original := &File{Filename: "prefix.elisa", Decls: []Decl{&NamespaceDecl{Name: "M", Decls: []Decl{nested}}, suffix}, DeclVisibility: map[Decl]string{nested: "public", suffix: "private"}}
	snapshot, err := NewSyntaxSnapshot(original, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := snapshot.CopyPrefix(1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.CopyPrefix(1)
	if err != nil {
		t.Fatal(err)
	}
	node := first.Decls[0].(*NamespaceDecl).Decls[0]
	if cap(first.Decls) != 1 || len(first.DeclVisibility) != 1 || first.DeclVisibility[node] != "public" || first.DeclVisibility[nested] != "" || node == second.Decls[0].(*NamespaceDecl).Decls[0] {
		t.Fatal("prefix retained suffix capacity, lost nested identity, or shared mutable declarations")
	}
	node.(*FuncDecl).Name = "changed"
	if second.Decls[0].(*NamespaceDecl).Decls[0].(*FuncDecl).Name != "same" {
		t.Fatal("prefix copies shared nested graph")
	}
}

func BenchmarkSyntaxReplayNodeTables(b *testing.B) {
	body := make([]Stmt, 256)
	for i := range body {
		body[i] = &ReturnStmt{Value: &BinaryExpr{Left: &Ident{Name: "x"}, Right: &IntLit{Value: "1"}}}
	}
	fn := &FuncDecl{Name: "sample", Body: body}
	snapshot, err := NewSyntaxSnapshot(&File{Decls: []Decl{fn}, DeclVisibility: map[Decl]string{fn: "private"}}, 1<<24)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copied, err := snapshot.CopyFile()
		if err != nil || len(copied.Decls[0].(*FuncDecl).Body) != 256 {
			b.Fatal("invalid replay", err)
		}
	}
}

func TestSyntaxSnapshotReplayTablesDoNotOwnReturnedGraphs(t *testing.T) {
	shared := &IntLit{Value: "17"}
	fn := &FuncDecl{Name: "sample", Body: []Stmt{&ReturnStmt{Value: &BinaryExpr{Left: shared, Right: shared}}}}
	snapshot, err := NewSyntaxSnapshot(&File{Decls: []Decl{fn}, DeclVisibility: map[Decl]string{fn: "private"}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var retained []*File
	for i := 0; i < 64; i++ {
		file, err := snapshot.CopyFile()
		if err != nil {
			t.Fatal(err)
		}
		decl := file.Decls[0].(*FuncDecl)
		value := decl.Body[0].(*ReturnStmt).Value.(*BinaryExpr)
		if value.Left != value.Right || value.Left == shared || file.DeclVisibility[decl] != "private" {
			t.Fatal("replay sharing or identity changed")
		}
		retained = append(retained, file)
	}
	for _, file := range retained {
		decl := file.Decls[0].(*FuncDecl)
		value := decl.Body[0].(*ReturnStmt).Value.(*BinaryExpr)
		if value.Left.(*IntLit).Value != "17" {
			t.Fatal("later copy changed retained graph")
		}
		value.Left.(*IntLit).Value = "changed"
	}
}

func BenchmarkSyntaxReplaySmallAndMixed(b *testing.B) {
	small, err := NewSyntaxSnapshot(&File{Decls: []Decl{&ConstDecl{Name: "x", Value: &IntLit{Value: "1"}}}}, 1<<24)
	if err != nil {
		b.Fatal(err)
	}
	body := make([]Stmt, 256)
	for i := range body {
		body[i] = &ReturnStmt{Value: &BinaryExpr{Left: &Ident{Name: "x"}, Right: &IntLit{Value: "1"}}}
	}
	large, err := NewSyntaxSnapshot(&File{Decls: []Decl{&FuncDecl{Name: "large", Body: body}}}, 1<<24)
	if err != nil {
		b.Fatal(err)
	}
	for _, mixed := range []bool{false, true} {
		label := "small"
		if mixed {
			label = "mixed"
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				snapshot := small
				if mixed && i%32 == 0 {
					snapshot = large
				}
				if _, err := snapshot.CopyFile(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTransientSyntaxSnapshotSmallBody(b *testing.B) {
	file := &File{Decls: []Decl{&FuncDecl{Name: "f", Body: []Stmt{&ReturnStmt{Value: &Ident{Name: "x"}}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NewTransientSyntaxSnapshot(file, 1<<20); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransientSyntaxSnapshotSingleCall(b *testing.B) {
	file := &File{Decls: []Decl{&FuncDecl{Name: "f", Body: []Stmt{&ReturnStmt{Value: &CallExpr{Func: &Ident{Name: "x"}}}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NewTransientSyntaxSnapshot(file, 1<<20); err != nil {
			b.Fatal(err)
		}
	}
}
