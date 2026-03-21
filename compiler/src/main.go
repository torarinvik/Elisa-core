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
	if options.filter != "" && !emitSupportsFilter(options.emit) {
		fmt.Fprintf(stderr, "error: -filter is only supported for -emit %s\n", supportedFilterEmitModes())
		return 1
	}

	src, err := readSourceWithIncludes(options.filename, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	file, result, ok := analyzeProgram(options.filename, src, stderr)
	if !ok {
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
	case emitTests, emitBenches, emitFixtures:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", options.emit)
			return 1
		}
		printAnnotatedFunctions(stdout, result, options.emit, options.filter)
		return 0
	case emitTestRunner:
		runnerSource, err := generateTestRunnerSource(options.filename, result, options.filter)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := os.WriteFile(options.output, []byte(runnerSource), 0o644); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, runnerSource)
		}
		return 0
	case emitTest:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitTest)
			return 1
		}
		return executeSelectedTests(options.filename, result, options.filter, effectiveOptimizationLevel(options), options.packedABI, stdout, stderr)
	case emitLLVM:
		output, err := backend.GenerateLLVMIRWithOptAndPackedABI(result, effectiveOptimizationLevel(options), options.packedABI)
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
		if err := backend.WriteLLVMBitcodeFileWithOptAndPackedABI(result, outputPathForEmit(options.filename, options.output, ".bc"), effectiveOptimizationLevel(options), options.packedABI); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitObject:
		if err := backend.WriteLLVMObjectFileWithOptAndPackedABI(result, outputPathForEmit(options.filename, options.output, ".o"), effectiveOptimizationLevel(options), options.packedABI); err != nil {
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

func analyzeProgram(filename string, src []byte, stderr io.Writer) (*ast.File, *semantic.Result, bool) {
	l := lexer.New(filename, src)
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, nil, false
	}

	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, nil, false
	}

	result := semantic.Analyze(file)
	if warns := result.Warnings(); len(warns) > 0 {
		for _, w := range warns {
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
	if errs := result.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, nil, false
	}

	return file, result, true
}

const (
	emitAST        = "ast"
	emitTests      = "tests"
	emitBenches    = "benches"
	emitFixtures   = "fixtures"
	emitTest       = "test"
	emitTestRunner = "test-runner"
	emitLLVM       = "llvm"
	emitHeader     = "header"
	emitBitcode    = "bc"
	emitObject     = "obj"
)

type cliOptions struct {
	emit        string
	filename    string
	output      string
	filter      string
	packedABI   backend.PackedEnumABI
	optLevel    backend.OptimizationLevel
	hasOptLevel bool
}

func parseArgs(args []string) (cliOptions, error) {
	options := cliOptions{emit: emitAST, packedABI: backend.PackedEnumABIRowHandle}
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
		case strings.HasPrefix(arg, "-filter="):
			options.filter = strings.TrimSpace(strings.TrimPrefix(arg, "-filter="))
		case arg == "-filter":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -filter")
			}
			options.filter = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-packed-abi="):
			abi, err := backend.ParsePackedEnumABI(strings.TrimSpace(strings.TrimPrefix(arg, "-packed-abi=")))
			if err != nil {
				return cliOptions{}, err
			}
			options.packedABI = abi
		case arg == "-packed-abi":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -packed-abi")
			}
			abi, err := backend.ParsePackedEnumABI(strings.TrimSpace(args[i]))
			if err != nil {
				return cliOptions{}, err
			}
			options.packedABI = abi
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
	fmt.Fprintf(w, "Usage: llcontext [-emit %s|%s|%s|%s|%s|%s|%s|%s|%s|%s] [-filter <substring>] [-packed-abi %s|%s] [-O0|-O2|-O3] [-o <output>] <file.llcontext>\n", emitAST, emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner, emitLLVM, emitHeader, emitBitcode, emitObject, backend.PackedEnumABIRowHandle, backend.PackedEnumABIWordHandle)
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
	case emitTests, "test-list":
		return emitTests
	case emitBenches, "bench-list":
		return emitBenches
	case emitFixtures, "fixture-list":
		return emitFixtures
	case emitTest, "run-test", "run-tests":
		return emitTest
	case emitTestRunner, "runner":
		return emitTestRunner
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

func emitSupportsFilter(emit string) bool {
	switch emit {
	case emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner:
		return true
	default:
		return false
	}
}

func supportedFilterEmitModes() string {
	return fmt.Sprintf("%s, %s, %s, %s, or %s", emitTests, emitBenches, emitFixtures, emitTestRunner, emitTest)
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

func printAnnotatedFunctions(w io.Writer, result *semantic.Result, emitMode string, filter string) {
	annotationName := annotationNameForEmitMode(emitMode)
	for _, fn := range selectAnnotatedFunctions(result, annotationName, filter) {
		signature := "<missing-signature>"
		if fn.Signature != nil {
			signature = fn.Signature.String()
		}
		fmt.Fprintf(w, "%s\t%s\n", fn.Name, signature)
	}
}

func annotationNameForEmitMode(emitMode string) string {
	switch emitMode {
	case emitTests:
		return "test"
	case emitBenches:
		return "bench"
	case emitFixtures:
		return "fixture"
	default:
		return ""
	}
}

func hasAnnotation(fn *semantic.AnnotatedFunc, annotationName string) bool {
	if fn == nil || annotationName == "" {
		return false
	}
	for _, annotation := range fn.Annotations {
		if annotation.Name == annotationName {
			return true
		}
	}
	return false
}

func ind(level int) string {
	return strings.Repeat("  ", level)
}

func formatFuncGenericParams(typeParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(typeParams)+len(regionParams)+len(permissionParams))
	parts = append(parts, typeParams...)
	for _, name := range regionParams {
		parts = append(parts, "region "+name)
	}
	for _, name := range permissionParams {
		parts = append(parts, "permission "+name)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatPermissionRefs(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Member != "" {
			parts = append(parts, ref.Name+"."+ref.Member)
			continue
		}
		parts = append(parts, ref.Name)
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}

func formatAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}

func printDecl(w io.Writer, d ast.Decl, level int) {
	prefix := ind(level)
	switch n := d.(type) {
	case *ast.PermissionDecl:
		fmt.Fprintf(w, "%spermission %s: (%d members)\n", prefix, n.Name, len(n.Members))
	case *ast.ConstDecl:
		fmt.Fprintf(w, "%sconst %s = %s\n", prefix, n.Name, exprStr(n.Value))
	case *ast.ConstEnumDecl:
		fmt.Fprintf(w, "%sconst enum %s of %s: (%d members)\n", prefix, n.Name, typeStr(n.Storage), len(n.Members))
	case *ast.GlobalDecl:
		mut := ""
		if n.Mutable {
			mut = "mutable "
		}
		fmt.Fprintf(w, "%sglobal %s%s: %s\n", prefix, mut, n.Name, typeStr(n.Type))
	case *ast.StructDecl:
		affine := ""
		if n.Affine {
			affine = "affine "
		}
		tparams := ""
		if len(n.TypeParams) > 0 {
			tparams = "[" + strings.Join(n.TypeParams, ", ") + "]"
		}
		fmt.Fprintf(w, "%s%sstruct %s%s (%d fields)\n", prefix, affine, n.Name, tparams, len(n.Fields))
	case *ast.FuncDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		tparams := formatFuncGenericParams(n.TypeParams, n.RegionParams, n.PermissionParams)
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sdef %s%s(%d params)%s%s (%d stmts)\n", prefix, n.Name, tparams, len(n.Params), ret, formatPermissionRefs(n.Permissions), len(n.Body))
	case *ast.ExternFuncDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		tparams := formatFuncGenericParams(nil, n.RegionParams, nil)
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sextern %s%s(%d params)%s%s\n", prefix, n.Name, tparams, len(n.Params), ret, formatPermissionRefs(n.Permissions))
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
		prefix := ""
		if n.Region != "" {
			prefix = n.Region + " "
		} else if n.Explicit || n.Storage != ast.RefStorageAny {
			switch n.Storage {
			case ast.RefStorageHeap:
				prefix = "heap "
			case ast.RefStorageStack:
				prefix = "stack "
			case ast.RefStorageStatic:
				prefix = "static "
			default:
				prefix = "any "
			}
		}
		switch n.State {
		case ast.RefStateNullable:
			return prefix + s + "&?"
		case ast.RefStateNull:
			return prefix + s + "!"
		default:
			return prefix + s + "&"
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
	case *ast.FuncTypeExpr:
		parts := make([]string, 0, len(n.Params))
		for _, param := range n.Params {
			parts = append(parts, typeStr(param))
		}
		ret := ""
		if n.Return != nil {
			ret = " -> " + typeStr(n.Return)
		}
		can := formatPermissionRefs(n.Permissions)
		return "func(" + strings.Join(parts, ", ") + ")" + ret + can
	case *ast.OptionalTypeExpr:
		return typeStr(n.Value) + "?"
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
		for i, a := range n.Args {
			if name := n.ArgName(i); name != "" {
				args = append(args, name+": "+exprStr(a))
				continue
			}
			args = append(args, exprStr(a))
		}
		return fmt.Sprintf("%s(%s)", exprStr(n.Func), strings.Join(args, ", "))
	case *ast.AllocExpr:
		if n.Owner == nil {
			return fmt.Sprintf("new %s", exprStr(n.Value))
		}
		return fmt.Sprintf("new[%s] %s", exprStr(n.Owner), exprStr(n.Value))
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
	case *ast.MoveExpr:
		return fmt.Sprintf("move %s", exprStr(n.Operand))
	case *ast.SpecializeExpr:
		typeArgs := make([]string, 0, len(n.TypeArgs))
		for _, arg := range n.TypeArgs {
			typeArgs = append(typeArgs, typeStr(arg))
		}
		return fmt.Sprintf("%s.specialize[%s]()", exprStr(n.Operand), strings.Join(typeArgs, ", "))
	case *ast.StructLitExpr:
		var args []string
		for _, a := range n.Args {
			args = append(args, exprStr(a))
		}
		return fmt.Sprintf("%s(%s)", n.Name, strings.Join(args, ", "))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", exprStr(n.Inner))
	case *ast.CanExpr:
		return exprStr(n.Expr) + formatPermissionRefs(n.Permissions)
	default:
		return "<expr>"
	}
}
