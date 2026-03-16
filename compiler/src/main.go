package main

import (
	"fmt"
	"io"
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
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		printUsage(stderr)
		return 1
	}
	return runWithOptions(options, stdout, stderr)
}

func runWithOptions(options cliOptions, stdout io.Writer, stderr io.Writer) int {
	src, err := readSourceWithIncludes(options.filename, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	l := lexer.New(options.filename, src)
	tokens := l.Tokenize()

	p := parser.New(tokens)
	file := p.ParseFile(options.filename)

	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return 1
	}

	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return 1
	}

	switch options.emit {
	case emitAST:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitAST)
			return 1
		}
		printFile(stdout, file)
		return 0
	case emitLLVM:
		output, err := backend.GenerateLLVMIRWithOpt(result, effectiveOptimizationLevel(options))
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := os.WriteFile(options.output, []byte(output), 0o644); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, output)
		}
		return 0
	case emitHeader:
		output, err := backend.GenerateCHeader(result)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := os.WriteFile(options.output, []byte(output), 0o644); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, output)
		}
		return 0
	case emitBitcode:
		if err := backend.WriteLLVMBitcodeFileWithOpt(result, outputPathForEmit(options.filename, options.output, ".bc"), effectiveOptimizationLevel(options)); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitObject:
		if err := backend.WriteLLVMObjectFileWithOpt(result, outputPathForEmit(options.filename, options.output, ".o"), effectiveOptimizationLevel(options)); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "error: unsupported emit mode %q\n", options.emit)
		printUsage(stderr)
		return 1
	}
}

const (
	emitAST     = "ast"
	emitLLVM    = "llvm"
	emitHeader  = "header"
	emitBitcode = "bc"
	emitObject  = "obj"
)

type cliOptions struct {
	emit        string
	filename    string
	output      string
	optLevel    backend.OptimizationLevel
	hasOptLevel bool
}

func parseArgs(args []string) (cliOptions, error) {
	options := cliOptions{emit: emitAST}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-O0" || arg == "-O2" || arg == "-O3":
			level, err := parseOptimizationArg(strings.TrimPrefix(arg, "-O"))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case strings.HasPrefix(arg, "-O="):
			level, err := parseOptimizationArg(strings.TrimSpace(strings.TrimPrefix(arg, "-O=")))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case arg == "-O":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -O")
			}
			level, err := parseOptimizationArg(strings.TrimSpace(args[i]))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
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

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage: llcontext [-emit %s|%s|%s|%s|%s] [-O0|-O2|-O3] [-o <output>] <file.llcontext>\n", emitAST, emitLLVM, emitHeader, emitBitcode, emitObject)
}

func parseOptimizationArg(value string) (backend.OptimizationLevel, error) {
	switch strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(value), "O")) {
	case "0":
		return backend.OptimizationLevel0, nil
	case "2":
		return backend.OptimizationLevel2, nil
	case "3":
		return backend.OptimizationLevel3, nil
	default:
		return 0, fmt.Errorf("unsupported optimization level %q (expected O0, O2, or O3)", value)
	}
}

func effectiveOptimizationLevel(options cliOptions) backend.OptimizationLevel {
	if options.hasOptLevel {
		return options.optLevel
	}
	switch options.emit {
	case emitBitcode, emitObject:
		return backend.OptimizationLevel3
	default:
		return backend.OptimizationLevel0
	}
}

func normalizeEmitMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case emitAST:
		return emitAST
	case emitLLVM:
		return emitLLVM
	case emitHeader:
		return emitHeader
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

func printFile(w io.Writer, f *ast.File) {
	fmt.Fprintf(w, "File: %s (%d declarations)\n", f.Filename, len(f.Decls))
	for _, d := range f.Decls {
		printDecl(w, d, 0)
	}
}

func ind(level int) string {
	return strings.Repeat("  ", level)
}

func printDecl(w io.Writer, d ast.Decl, level int) {
	prefix := ind(level)
	switch n := d.(type) {
	case *ast.ConstDecl:
		fmt.Fprintf(w, "%sconst %s = %s\n", prefix, n.Name, exprStr(n.Value))
	case *ast.GlobalDecl:
		mut := ""
		if n.Mutable {
			mut = "mutable "
		}
		fmt.Fprintf(w, "%sglobal %s%s: %s\n", prefix, mut, n.Name, typeStr(n.Type))
	case *ast.StructDecl:
		repr := ""
		if n.ReprC {
			repr = "repr(c) "
		}
		tparams := ""
		if len(n.TypeParams) > 0 {
			tparams = "[" + strings.Join(n.TypeParams, ", ") + "]"
		}
		fmt.Fprintf(w, "%s%sstruct %s%s (%d fields)\n", prefix, repr, n.Name, tparams, len(n.Fields))
	case *ast.FuncDecl:
		tparams := ""
		if len(n.TypeParams) > 0 {
			tparams = "[" + strings.Join(n.TypeParams, ", ") + "]"
		}
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sdef %s%s(%d params)%s (%d stmts)\n", prefix, n.Name, tparams, len(n.Params), ret, len(n.Body))
	case *ast.ExternFuncDecl:
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sextern %s(%d params)%s\n", prefix, n.Name, len(n.Params), ret)
	case *ast.ExternVarDecl:
		fmt.Fprintf(w, "%sextern %s: %s\n", prefix, n.Name, typeStr(n.Type))
	case *ast.ExternTypeDecl:
		fmt.Fprintf(w, "%sextern type %s\n", prefix, n.Name)
	case *ast.ExportTypeDecl:
		fmt.Fprintf(w, "%sexport type %s as %s\n", prefix, typeStr(n.ExportedType), n.Alias)
	case *ast.ExportFuncDecl:
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		target := n.TargetName
		if len(n.TargetTypeArgs) > 0 {
			parts := make([]string, 0, len(n.TargetTypeArgs))
			for _, arg := range n.TargetTypeArgs {
				parts = append(parts, typeStr(arg))
			}
			target += "[" + strings.Join(parts, ", ") + "]"
		}
		fmt.Fprintf(w, "%sexport func %s(%d params)%s = %s\n", prefix, n.Name, len(n.Params), ret, target)
	case *ast.ExportGlobalDecl:
		fmt.Fprintf(w, "%sexport global %s as %s\n", prefix, n.TargetName, n.Alias)
	case *ast.StaticIfDecl:
		fmt.Fprintf(w, "%sstatic if %s: (%d then, %d elifs)\n", prefix, exprStr(n.Cond), len(n.Then), len(n.Elifs))
		for _, then := range n.Then {
			printDecl(w, then, level+1)
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
	case *ast.BuiltinTypeExpr:
		parts := make([]string, 0, len(n.TypeArgs)+len(n.ValueArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, typeStr(arg))
		}
		for _, arg := range n.ValueArgs {
			parts = append(parts, exprStr(arg))
		}
		return n.Name + "[" + strings.Join(parts, ", ") + "]"
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
	case *ast.ListLitExpr:
		var elems []string
		for _, elem := range n.Elems {
			elems = append(elems, exprStr(elem))
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ", "))
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
	case *ast.SliceExpr:
		return fmt.Sprintf("%s[%s:%s]", exprStr(n.Object), exprStr(n.Start), exprStr(n.End))
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
