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
			copy, err := snapshot.CopyFile()
			if err != nil {
				t.Error(err)
				return
			}
			if !reflect.DeepEqual(copy, file) {
				t.Error("copy changed graph")
			}
			copy.Decls[0].(*ConstDecl).Name = "changed"
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
