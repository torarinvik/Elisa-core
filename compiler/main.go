package main

import (
	"fmt"
	"llcontext/ast"
	"llcontext/lexer"
	"llcontext/parser"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: llcontext <file.llcontext>\n")
		os.Exit(1)
	}

	filename := os.Args[1]
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	l := lexer.New(filename, src)
	tokens := l.Tokenize()

	p := parser.New(tokens)
	file := p.ParseFile(filename)

	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		os.Exit(1)
	}

	printFile(file)
}

func printFile(f *ast.File) {
	fmt.Printf("File: %s (%d declarations)\n", f.Filename, len(f.Decls))
	for _, d := range f.Decls {
		printDecl(d, 0)
	}
}

func ind(level int) string {
	return strings.Repeat("  ", level)
}

func printDecl(d ast.Decl, level int) {
	prefix := ind(level)
	switch n := d.(type) {
	case *ast.ConstDecl:
		fmt.Printf("%sconst %s = %s\n", prefix, n.Name, exprStr(n.Value))
	case *ast.GlobalDecl:
		mut := ""
		if n.Mutable {
			mut = "mutable "
		}
		fmt.Printf("%sglobal %s%s: %s\n", prefix, mut, n.Name, typeStr(n.Type))
	case *ast.StructDecl:
		repr := ""
		if n.ReprC {
			repr = "repr(c) "
		}
		tparams := ""
		if len(n.TypeParams) > 0 {
			tparams = "[" + strings.Join(n.TypeParams, ", ") + "]"
		}
		fmt.Printf("%s%sstruct %s%s (%d fields)\n", prefix, repr, n.Name, tparams, len(n.Fields))
	case *ast.FuncDecl:
		tparams := ""
		if len(n.TypeParams) > 0 {
			tparams = "[" + strings.Join(n.TypeParams, ", ") + "]"
		}
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Printf("%sdef %s%s(%d params)%s (%d stmts)\n", prefix, n.Name, tparams, len(n.Params), ret, len(n.Body))
	case *ast.ExternFuncDecl:
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Printf("%sextern %s(%d params)%s\n", prefix, n.Name, len(n.Params), ret)
	case *ast.ExternVarDecl:
		fmt.Printf("%sextern %s: %s\n", prefix, n.Name, typeStr(n.Type))
	case *ast.ExternTypeDecl:
		fmt.Printf("%sextern type %s\n", prefix, n.Name)
	case *ast.StaticIfDecl:
		fmt.Printf("%sstatic if %s: (%d then, %d elifs)\n", prefix, exprStr(n.Cond), len(n.Then), len(n.Elifs))
		for _, then := range n.Then {
			printDecl(then, level+1)
		}
	}
}

func typeStr(t ast.TypeExpr) string {
	if t == nil {
		return "<nil>"
	}
	switch n := t.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.RefType:
		s := typeStr(n.Elem) + "&"
		if n.Nullable {
			s += "?"
		}
		return s
	case *ast.GenericType:
		var args []string
		for _, a := range n.Args {
			args = append(args, typeStr(a))
		}
		return n.Name + "[" + strings.Join(args, ", ") + "]"
	case *ast.MutableType:
		return "mutable " + typeStr(n.Elem)
	case *ast.TailType:
		return "tail " + typeStr(n.Elem)
	case *ast.ArrayType:
		return typeStr(n.Elem) + "[" + exprStr(n.Size) + "]"
	default:
		return "<type>"
	}
}

func exprStr(e ast.Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		s := n.Value
		if n.Suffix != "" {
			s += n.Suffix
		}
		return s
	case *ast.StringLit:
		return fmt.Sprintf("%q", n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", exprStr(n.Left), lexer.TokenName(n.Op), exprStr(n.Right))
	case *ast.UnaryExpr:
		return fmt.Sprintf("(%s %s)", lexer.TokenName(n.Op), exprStr(n.Operand))
	case *ast.CallExpr:
		var args []string
		for _, a := range n.Args {
			args = append(args, exprStr(a))
		}
		return fmt.Sprintf("%s(%s)", exprStr(n.Func), strings.Join(args, ", "))
	case *ast.FieldExpr:
		return fmt.Sprintf("%s.%s", exprStr(n.Object), n.Field)
	case *ast.IndexExpr:
		return fmt.Sprintf("%s[%s]", exprStr(n.Object), exprStr(n.Index))
	case *ast.CastExpr:
		return fmt.Sprintf("%s.%s()", exprStr(n.Operand), typeStr(n.Target))
	case *ast.SizeofExpr:
		return fmt.Sprintf("sizeof(%s)", typeStr(n.Type))
	case *ast.TernaryExpr:
		return fmt.Sprintf("(%s if %s else %s)", exprStr(n.Value), exprStr(n.Cond), exprStr(n.Alt))
	case *ast.AddrOfExpr:
		return fmt.Sprintf("&%s", exprStr(n.Operand))
	case *ast.StructLitExpr:
		var args []string
		for _, a := range n.Args {
			args = append(args, exprStr(a))
		}
		return fmt.Sprintf("%s(%s)", n.Name, strings.Join(args, ", "))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", exprStr(n.Inner))
	default:
		return "<expr>"
	}
}
