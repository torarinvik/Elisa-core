package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/frontendir"
	"elisacore/src/semantic"
)

const (
	sourceExtension     = ".elisa"
	interfaceExtension  = ".elisai"
	frontendIRExtension = ".elisair"
	loweredExtension    = ".lowered.elisa"
)

func isSurfaceSourcePath(path string) bool {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
	return ext == sourceExtension || ext == interfaceExtension
}

type loadedProgram struct {
	tokenCache  *sourceTokenCache
	filename    string
	source      []byte
	file        *ast.File
	easm        []*easm.Module
	sourceFiles []string
}

func loadProgramInput(filename string, stderr io.Writer) (*loadedProgram, bool) {
	if strings.EqualFold(filepath.Ext(filename), frontendIRExtension) {
		data, err := os.ReadFile(filename)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return nil, false
		}
		bundle, err := frontendir.Decode(data)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return nil, false
		}
		loadedFilename := bundle.SourceFilename
		if strings.TrimSpace(loadedFilename) == "" {
			loadedFilename = filename
		}
		return &loadedProgram{
			filename: loadedFilename,
			source:   append([]byte(nil), bundle.ResolvedSource...),
			file:     bundle.File,
		}, true
	}
	seen := map[string]bool{}
	src, err := readSourceWithIncludes(filename, seen)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return nil, false
	}
	sourceFiles := make([]string, 0, len(seen))
	for path := range seen {
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			sourceFiles = append(sourceFiles, filepath.Clean(canonical))
		} else {
			sourceFiles = append(sourceFiles, filepath.Clean(path))
		}
	}
	return &loadedProgram{filename: filename, source: src, sourceFiles: sourceFiles}, true
}

func parseLoadedProgram(program *loadedProgram, stderr io.Writer) (*ast.File, bool) {
	if program == nil {
		fmt.Fprintf(stderr, "error: missing program input\n")
		return nil, false
	}
	if program.file != nil {
		return program.file, true
	}
	return parseProgramWithTokenCache(program.filename, program.source, stderr, program.tokenCache)
}

func analyzeLoadedProgram(program *loadedProgram, stderr io.Writer) (*ast.File, *semantic.Result, bool) {
	return analyzeLoadedProgramWithOptions(program, stderr, semantic.AnalyzeOptions{})
}

func analyzeLoadedProgramWithOptions(program *loadedProgram, stderr io.Writer, options semantic.AnalyzeOptions) (*ast.File, *semantic.Result, bool) {
	return analyzeLoadedProgramWithSession(program, stderr, options, nil)
}

func analyzeSemanticWithSession(file *ast.File, options semantic.AnalyzeOptions, session *semantic.IncrementalAnalysisSession) *semantic.Result {
	if session != nil {
		return session.Analyze(file, options)
	}
	if activeRetained != nil {
		return activeRetained.analyze(file, options)
	}
	return semantic.AnalyzeWithOptions(file, options)
}

func analyzeLoadedProgramWithSession(program *loadedProgram, stderr io.Writer, options semantic.AnalyzeOptions, session *semantic.IncrementalAnalysisSession) (*ast.File, *semantic.Result, bool) {
	var pending *analysisDiskRestoration
	return analyzeLoadedProgramUsingPreparation(program, stderr, options, func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		if session == nil && activeRetained == nil && analysisDiskCacheEnabled() && program != nil && program.file == nil {
			return analyzeSemanticFromDisk(program, file, options, stderr, pending)
		}
		return analyzeSemanticWithSession(file, options, session)
	}, func(options semantic.AnalyzeOptions) func() {
		if session == nil && activeRetained == nil && analysisDiskCacheEnabled() {
			pending = startAnalysisDiskRestoration(program, options)
		}
		return pending.join
	})
}

// Keep provenance/options handling identical for the normal analyzer and
// opt-in dependency audits; an injected analyzer must still do full analysis.
func analyzeLoadedProgramUsing(program *loadedProgram, stderr io.Writer, options semantic.AnalyzeOptions, analyze func(*ast.File, semantic.AnalyzeOptions) *semantic.Result) (*ast.File, *semantic.Result, bool) {
	return analyzeLoadedProgramUsingPreparation(program, stderr, options, analyze, nil)
}

func analyzeLoadedProgramUsingPreparation(program *loadedProgram, stderr io.Writer, options semantic.AnalyzeOptions, analyze func(*ast.File, semantic.AnalyzeOptions) *semantic.Result, prepare func(semantic.AnalyzeOptions) func()) (*ast.File, *semantic.Result, bool) {
	if program == nil {
		fmt.Fprintf(stderr, "error: missing program input\n")
		return nil, nil, false
	}
	if program.file != nil {
		// A decoded frontend-IR AST does not carry the include loader's source
		// provenance, so it cannot authorize source-sensitive TaskGroup proofs.
		options.TrustedRuntimeSourceFiles = nil
		result := analyze(program.file, options)
		frontendSMTTimingLog(stderr, result)
		result.EASMModules = append([]*easm.Module(nil), program.easm...)
		if warns := result.Notices(); len(warns) > 0 {
			for _, w := range warns {
				if shouldSuppressDeprecatedWarningsForTests(w) {
					continue
				}
				fmt.Fprintf(stderr, "%s\n", w)
			}
		}
		if errs := result.Errors(); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
			return nil, nil, false
		}
		return program.file, result, true
	}
	if runtimeSource := compilerTaskGroupRuntimeSourcePath(); runtimeSource != "" {
		for _, sourceFile := range program.sourceFiles {
			if sourceFile == runtimeSource {
				options.TrustedRuntimeSourceFiles = append(options.TrustedRuntimeSourceFiles, runtimeSource)
				break
			}
		}
	}
	if requested := os.Getenv("ELISACORE_TRUSTED_RUNTIME_SOURCE"); requested != "" {
		runtimeSource, err := includedTrustedRuntimeSource(program, requested)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return nil, nil, false
		}
		options.TrustedRuntimeSourceFiles = append(options.TrustedRuntimeSourceFiles, runtimeSource)
	}
	// Source authority and final semantic options are established before any
	// speculative restore. Join even when the primary parse rejects the input.
	if prepare != nil {
		if join := prepare(options); join != nil {
			defer join()
		}
	}
	file, result, ok := analyzeProgramWithAnalyzerAndTokens(program.filename, program.source, stderr, options, program.tokenCache, func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		return analyze(file, options)
	})
	if ok && result != nil {
		result.EASMModules = append([]*easm.Module(nil), program.easm...)
	}
	return file, result, ok
}

// includedTrustedRuntimeSource resolves an explicitly trusted runtime path only when the
// include loader expanded that exact file. The Stage1 seed uses this to authorize its vendored
// standard-library copy; a declaration's filename or a same-named file elsewhere is insufficient.
func includedTrustedRuntimeSource(program *loadedProgram, requested string) (string, error) {
	if program == nil || strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("trusted runtime source must name an included source file")
	}
	canonical, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("resolve trusted runtime source %q: %w", requested, err)
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = filepath.Clean(resolved)
	}
	for _, sourceFile := range program.sourceFiles {
		if sourceFile == canonical {
			return canonical, nil
		}
	}
	return "", fmt.Errorf("trusted runtime source %q was not among the expanded includes", canonical)
}

func compilerTaskGroupRuntimeSourcePath() string {
	const relative = "runtime/elisacore_std/elisacore_runtime_concurrency.elisa"
	starts := make([]string, 0, 3)
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(sourceFile))
	}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}
	for _, start := range starts {
		if !filepath.IsAbs(start) {
			if absolute, err := filepath.Abs(start); err == nil {
				start = absolute
			}
		}
		for dir := start; dir != ""; dir = filepath.Dir(dir) {
			module, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err == nil && strings.HasPrefix(strings.TrimSpace(string(module)), "module elisacore") {
				candidate := filepath.Join(dir, filepath.FromSlash(relative))
				if canonical, err := filepath.EvalSymlinks(candidate); err == nil {
					return filepath.Clean(canonical)
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return ""
}

func buildFrontendIRBundle(program *loadedProgram, file *ast.File) *frontendir.Bundle {
	if program == nil {
		return nil
	}
	return &frontendir.Bundle{
		Version:        frontendir.BundleVersion,
		SourceFilename: program.filename,
		ResolvedSource: append([]byte(nil), program.source...),
		File:           file,
	}
}
