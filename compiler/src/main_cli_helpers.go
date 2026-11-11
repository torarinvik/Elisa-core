package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func printUsage(w io.Writer) {
	emitModes := []string{emitAST, emitLowered, emitSemantic, emitFacts, emitUnsafe, emitProgress, emitFmt, emitDoc, emitInterface, emitDeps, emitDepsJSON, emitIR, emitInterpret, emitServe, emitRetainedServe, emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner, emitLLVM, emitPacked, emitCBindCheck, emitCBindJSON, emitHeader, emitBitcode, emitObject, emitCArchive, emitTokens}
	fmt.Fprintf(w, "Usage: elisacore [-emit %s] [-addr <host:port>] [-filter <substring>] [-target-triple <llvm-triple>] [-O0|-O1|-O2|-O3] [-fprofile-use <profile>] [-o <output>] [-link <flag>|-L <dir>|-l <name>] <file%s|file%s|file%s>\n", strings.Join(emitModes, "|"), sourceExtension, interfaceExtension, frontendIRExtension)
	fmt.Fprintln(w, "       elisacore init <name> [--path <dir>] [--strict]")
	fmt.Fprintln(w, "       elisacore init-lib <name> [--path <dir>]")
	fmt.Fprintln(w, "       elisacore build|run|test|bench [target] [--project <dir|project.json>]")
	// `--json` is a deps/abi-lint/easm-lint option; `project view` accepts it silently
	// and ignores it. Grouping the subcommands on one line advertised a flag that does
	// nothing and hid abi-lint and easm-lint entirely — `elisacore project` prints the
	// accurate per-subcommand list, and this summary now agrees with it.
	fmt.Fprintln(w, "       elisacore project view [target] [--project <dir|project.json>]")
	fmt.Fprintln(w, "       elisacore project deps|abi-lint|easm-lint [target] [--project <dir|project.json>] [--json]")
	fmt.Fprintf(w, "Packed enums lower canonically as handle-based %s in compiler mode; use @packed_profile(canonical|retained_reads|build_heavy) for supported enum-level tuning.\n", backend.PackedEnumABIVariantSparse)
}
func parseOptimizationArg(value string) (backend.OptimizationLevel, error) {
	switch strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(value), "O")) {
	case "0":
		return backend.OptimizationLevel0, nil
	case "1":
		return backend.OptimizationLevel1, nil
	case "2":
		return backend.OptimizationLevel2, nil
	case "3":
		return backend.OptimizationLevel3, nil
	default:
		return 0, fmt.Errorf("unsupported optimization level %q (expected O0, O1, O2, or O3)", value)
	}
}
func effectiveOptimizationLevel(options cliOptions) backend.OptimizationLevel {
	if options.hasOptLevel {
		return options.optLevel
	}
	switch options.emit {
	case emitBitcode, emitObject, emitCArchive:
		return backend.OptimizationLevel3
	default:
		return backend.OptimizationLevel0
	}
}
func normalizeEmitMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case emitAST:
		return emitAST
	case emitLowered, "lower", "lowering", "grammar-lowered":
		return emitLowered
	case emitSemantic, "sema", "semantics", "lowered-semantic", "typed-report":
		return emitSemantic
	case emitFacts, "fact", "fact-trace", "trace-facts":
		return emitFacts
	case emitUnsafe, "unsafe-summary", "unsafe-report":
		return emitUnsafe
	case emitProgress, "progress-summary", "progress-report":
		return emitProgress
	case emitFmt, "format", "formatter":
		return emitFmt
	case emitDoc, "docs", "reference":
		return emitDoc
	case emitInterface, "interface", "api":
		return emitInterface
	case emitDeps, "dep", "dependencies":
		return emitDeps
	case emitDepsJSON, "depsjson", "dependencies-json":
		return emitDepsJSON
	case emitIR, "frontend-ir", "bundle":
		return emitIR
	case emitInterpret, "run", "interp":
		return emitInterpret
	case emitServe, "server":
		return emitServe
	case emitRetainedServe:
		return emitRetainedServe
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
	case emitPacked, "packed-info", "packedinfo":
		return emitPacked
	case emitCBindCheck, "cbind-check", "c-bind", "cbind":
		return emitCBindCheck
	case emitCBindJSON, "cbind-check-json", "c-bind-json", "cbind-json", "ffi-manifest", "abi-manifest":
		return emitCBindJSON
	case emitHeader:
		return emitHeader
	case emitBitcode, "bitcode":
		return emitBitcode
	case emitObject, "object":
		return emitObject
	case emitCArchive, "carchive", "archive", "static-archive", "staticlib", "static-lib":
		return emitCArchive
	case emitTokens, "token", "token-kinds", "lex":
		return emitTokens
	default:
		return strings.TrimSpace(value)
	}
}
func emitSupportsFilter(emit string) bool {
	switch emit {
	case emitFacts, emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner:
		return true
	default:
		return false
	}
}
func supportedFilterEmitModes() string {
	return fmt.Sprintf("%s, %s, %s, %s, %s, or %s", emitFacts, emitTests, emitBenches, emitFixtures, emitTestRunner, emitTest)
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
	var out sourceSplice
	if err := writeSourceWithIncludesLabels(&out, filename, seen, map[string]bool{}, nil, nil, sourceLabelResolver{}); err != nil {
		return nil, err
	}
	return out.bytes(), nil
}
func writeSourceWithIncludes(out *bytes.Buffer, filename string, seen map[string]bool) error {
	return writeSourceWithIncludesActive(out, filename, seen, map[string]bool{})
}

func writeSourceWithIncludesActive(out *bytes.Buffer, filename string, included map[string]bool, active map[string]bool) error {
	// A nil resolver option retains direct relative/absolute path resolution and
	// filesystem errors, while sharing the project's streaming splice logic.
	return writeSourceWithIncludesPrefixed(out, filename, included, active, nil, nil)
}

func leadingWhitespaceBytes(line []byte) []byte {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func parseIncludeDirective(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	}
	for _, prefix := range []string{"include "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return "", false
		}
		return rest[1 : len(rest)-1], true
	}
	if len(line) >= 4 && strings.HasPrefix(line, "{$") && strings.HasSuffix(line, "}") {
		body := strings.TrimSpace(line[2 : len(line)-1])
		keyword := body
		arg := ""
		if cut := strings.IndexFunc(body, unicode.IsSpace); cut >= 0 {
			keyword = body[:cut]
			arg = strings.TrimSpace(body[cut:])
		}
		switch strings.ToLower(keyword) {
		case "i", "include":
			if arg == "" {
				return "", false
			}
			if len(arg) >= 2 && ((arg[0] == '\'' && arg[len(arg)-1] == '\'') || (arg[0] == '"' && arg[len(arg)-1] == '"')) {
				return arg[1 : len(arg)-1], true
			}
			if strings.IndexFunc(arg, unicode.IsSpace) >= 0 {
				return "", false
			}
			return arg, true
		}
	}
	return "", false
}
func parseIncludeDirectiveBytes(line []byte) (string, bool) {
	// Most source lines are not include directives. Avoid allocating their
	// string copies; preserve the canonical parser for all possible forms.
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) != 0 && trimmed[0] == '#' {
		trimmed = bytes.TrimSpace(trimmed[1:])
	}
	if !bytes.HasPrefix(trimmed, []byte("include ")) && !bytes.HasPrefix(line, []byte("{$")) {
		return "", false
	}
	return parseIncludeDirective(string(line))
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
func formatFuncGenericParams(genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(genericParams)+len(typeParams)+len(regionParams)+len(permissionParams))
	if len(genericParams) != 0 {
		seenRegion := map[string]bool{}
		seenPermission := map[string]bool{}
		for _, param := range genericParams {
			switch param.Kind {
			case ast.GenericParamRegion:
				seenRegion[param.Name] = true
				parts = append(parts, "@"+param.Name)
			case ast.GenericParamPermission:
				seenPermission[param.Name] = true
				parts = append(parts, "permission "+param.Name)
			default:
				if param.InterfaceBound != "" {
					parts = append(parts, param.Name+": "+param.InterfaceBound)
				} else {
					parts = append(parts, param.Name)
				}
			}
		}
		for _, name := range regionParams {
			if !seenRegion[name] {
				parts = append(parts, "@"+name)
			}
		}
		for _, name := range permissionParams {
			if !seenPermission[name] {
				parts = append(parts, "permission "+name)
			}
		}
	} else {
		parts = append(parts, typeParams...)
		for _, name := range regionParams {
			parts = append(parts, "@"+name)
		}
		for _, name := range permissionParams {
			parts = append(parts, "permission "+name)
		}
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
		parts = append(parts, formatPermissionRef(ref))
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}
func formatPermissionRef(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}
func formatAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}
func aggregateStatePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Used before both the disk lookup and ordinary compilation, so their keys agree.
func configureCLIProfile(options cliOptions) error {
	profileNames := make([]string, 0, len(options.profileHotFunctions))
	for name := range options.profileHotFunctions {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	return os.Setenv("ELISACORE_PGO_HOT_FUNCTIONS", strings.Join(profileNames, "\n"))
}
