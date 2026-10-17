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
