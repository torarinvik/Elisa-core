package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	filename string
	source   []byte
	file     *ast.File
	easm     []*easm.Module
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
	src, err := readSourceWithIncludes(filename, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return nil, false
	}
	return &loadedProgram{filename: filename, source: src}, true
}

func parseLoadedProgram(program *loadedProgram, stderr io.Writer) (*ast.File, bool) {
	if program == nil {
		fmt.Fprintf(stderr, "error: missing program input\n")
		return nil, false
	}
	if program.file != nil {
		return program.file, true
	}
	return parseProgram(program.filename, program.source, stderr)
}

func analyzeLoadedProgram(program *loadedProgram, stderr io.Writer) (*ast.File, *semantic.Result, bool) {
	return analyzeLoadedProgramWithOptions(program, stderr, semantic.AnalyzeOptions{})
}

func analyzeLoadedProgramWithOptions(program *loadedProgram, stderr io.Writer, options semantic.AnalyzeOptions) (*ast.File, *semantic.Result, bool) {
	if program == nil {
		fmt.Fprintf(stderr, "error: missing program input\n")
		return nil, nil, false
	}
	if program.file != nil {
		result := semantic.AnalyzeWithOptions(program.file, options)
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
	file, result, ok := analyzeProgramWithOptions(program.filename, program.source, stderr, options)
	if ok && result != nil {
		result.EASMModules = append([]*easm.Module(nil), program.easm...)
	}
	return file, result, ok
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
