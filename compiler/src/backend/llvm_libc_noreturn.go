//go:build cgo

package backend

import "elisacore/src/ast"

// libcNoreturnSymbols are the C-standard functions declared `_Noreturn` in <stdlib.h> /
// <unistd.h>: true = `void f(int)`, false = `void f(void)`. Their names are reserved by the C
// standard, so an `extern` binding one of them with the matching C signature IS the libc
// function, and `noreturn` is a sound codegen fact (clang takes it from the headers). Without it
// a die-path `exit(1)` rejoins the hot path and forces header reloads and repeated bounds
// checks at the join (AWFY Towers: -19% instructions). This is not the language-level
// `-> never` feature: user externs are untouched.
var libcNoreturnSymbols = map[string]bool{
	"exit":       true,
	"_exit":      true,
	"_Exit":      true,
	"quick_exit": true,
	"abort":      false,
}

// applyLibcNoreturnAttr marks fn noreturn when it is an `extern` whose linker symbol is a
// C-standard noreturn function and whose lowered type matches the C signature exactly.
func (g *llvmGenerator) applyLibcNoreturnAttr(fn llvmIRValueRef, elisaName, symbolName string, fnType llvmIRTypeRef) {
	wantIntParam, ok := libcNoreturnSymbols[symbolName]
	if !ok || fn == nil || !g.isExternFuncDecl(elisaName) {
		return
	}
	want := llvmIRInt(0)
	if wantIntParam {
		want = 1
	}
	if llvmLibcNoreturnSignature(fnType, want) == 0 {
		return
	}
	llvmAddNoreturnAttr(g.context, fn)
}

func (g *llvmGenerator) isExternFuncDecl(name string) bool {
	if g == nil || g.result == nil || !g.hasGlobalScope() {
		return false
	}
	sym, ok := g.lookupGlobal(name)
	if !ok || sym == nil {
		return false
	}
	decl, ok := sym.Node.(*ast.ExternFuncDecl)
	return ok && decl != nil
}
