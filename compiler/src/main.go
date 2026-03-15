package main

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/backend"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	options, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		printUsage()
		os.Exit(1)
	}

	src, err := readSourceWithIncludes(options.filename, map[string]bool{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	l := lexer.New(options.filename, src)
	tokens := l.Tokenize()

	p := parser.New(tokens)
	file := p.ParseFile(options.filename)

	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		os.Exit(1)
	}

	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		os.Exit(1)
	}

	switch options.emit {
	case emitAST:
		if options.output != "" {
			fmt.Fprintf(os.Stderr, "error: -o is not supported for -emit %s\n", emitAST)
			os.Exit(1)
		}
		printFile(file)
	case emitLLVM:
		output, err := backend.GenerateLLVMIR(result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		if options.output != "" {
			if err := os.WriteFile(options.output, []byte(output), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Print(output)
		}
	case emitBitcode:
		if err := backend.WriteLLVMBitcodeFile(result, outputPathForEmit(options.filename, options.output, ".bc")); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
	case emitObject:
		if err := backend.WriteLLVMObjectFile(result, outputPathForEmit(options.filename, options.output, ".o")); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: unsupported emit mode %q\n", options.emit)
		printUsage()
		os.Exit(1)
	}
}

const (
	emitAST     = "ast"
	emitLLVM    = "llvm"
	emitBitcode = "bc"
	emitObject  = "obj"
)

type cliOptions struct {
	emit     string
	filename string
	output   string
}

func parseArgs(args []string) (cliOptions, error) {
	options := cliOptions{emit: emitAST}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "-emit="):
			options.emit = strings.TrimSpace(strings.TrimPrefix(arg, "-emit="))
		case arg == "-emit":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -emit")
			}
			options.emit = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-o="):
			options.output = strings.TrimSpace(strings.TrimPrefix(arg, "-o="))
		case arg == "-o":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -o")
			}
			options.output = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-"):
			return cliOptions{}, fmt.Errorf("unknown option %q", arg)
		default:
			if options.filename != "" {
				return cliOptions{}, fmt.Errorf("expected a single input file, got %q", arg)
			}
			options.filename = arg
		}
	}
	if options.filename == "" {
		return cliOptions{}, fmt.Errorf("missing input file")
	}
	options.emit = normalizeEmitMode(options.emit)
	if options.emit == "" {
		return cliOptions{}, fmt.Errorf("emit mode cannot be empty")
	}
	return options, nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: llcontext [-emit %s|%s|%s|%s] [-o <output>] <file.llcontext>\n", emitAST, emitLLVM, emitBitcode, emitObject)
}

func normalizeEmitMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case emitAST:
		return emitAST
	case emitLLVM:
		return emitLLVM
	case emitBitcode, "bitcode":
		return emitBitcode
	case emitObject, "object":
		return emitObject
	default:
		return strings.TrimSpace(value)
	}
}

func outputPathForEmit(inputPath string, explicit string, ext string) string {
	if explicit != "" {
		return explicit
	}
	base := strings.TrimSuffix(inputPath, filepath.Ext(inputPath))
	if base == "" {
		return inputPath + ext
	}
	return base + ext
}

func readSourceWithIncludes(filename string, seen map[string]bool) ([]byte, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if seen[abs] {
		return nil, fmt.Errorf("cyclic include detected for %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(raw), "\n")
	var out strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if includePath, ok := parseIncludeDirective(trimmed); ok {
			included, err := readSourceWithIncludes(filepath.Join(filepath.Dir(abs), includePath), seen)
			if err != nil {
				return nil, err
			}
			out.Write(included)
			if len(included) == 0 || included[len(included)-1] != '\n' {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return []byte(out.String()), nil
}

func parseIncludeDirective(line string) (string, bool) {
	if !strings.HasPrefix(line, "# include ") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "# include "))
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false
	}
	return rest[1 : len(rest)-1], true
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
		s := typeStr(n.Elem)
		switch n.State {
		case ast.RefStateNullable:
			return s + "&?"
		case ast.RefStateNull:
			return s + "!"
		default:
			return s + "&"
		}
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
