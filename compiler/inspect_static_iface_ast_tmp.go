package main

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"os"
)

func dumpExpr(label string, expr ast.Expr, indent string) {
	if expr == nil {
		fmt.Printf("%s%s: <nil>\n", indent, label)
		return
	}
	fmt.Printf("%s%s: %T\n", indent, label, expr)
	switch n := expr.(type) {
	case *ast.CallExpr:
		dumpExpr("Func", n.Func, indent+"  ")
	case *ast.FieldExpr:
		dumpExpr("Object", n.Object, indent+"  ")
		fmt.Printf("%s  Field: %s\n", indent, n.Field)
	case *ast.Ident:
		fmt.Printf("%s  Name: %s\n", indent, n.Name)
	case *ast.ParenExpr:
		dumpExpr("Inner", n.Inner, indent+"  ")
	}
}

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	l := lexer.New(os.Args[1], data)
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		panic(errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(os.Args[1])
	if errs := p.Errors(); len(errs) > 0 {
		panic(errs)
	}
	fn := file.Decls[3].(*ast.FuncDecl)
	ret := fn.Body[0].(*ast.ReturnStmt)
	dumpExpr("Return", ret.Value, "")
}
