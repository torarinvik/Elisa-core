package main

import (
	"bytes"
	"elisacore/src/lexer"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Runtime-carrier diagnostics distinguish synthetic bare names from existing
// source paths. #line attribution can select names other than the request name.
type diagnosticPathState struct {
	Path   string
	Exists bool
}

func diagnosticPathInputs(filename string, source []byte) []diagnosticPathState {
	names := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "<unknown>" && !strings.Contains(filepath.ToSlash(name), "/") {
			names[name] = true
		}
	}
	add(filename)
	// Ordinary source needs no tokenization for response admission. For possible
	// attribution directives use the actual lexer, keeping comment/string syntax
	// and directive parsing identical to the compiler rather than duplicating it.
	if bytes.Contains(source, []byte("#line")) {
		scanner := lexer.New(filename, source)
		for {
			token := scanner.NextToken()
			add(token.Pos.File)
			if token.Kind == lexer.TOKEN_EOF {
				break
			}
		}
	}
	paths := make([]string, 0, len(names))
	for name := range names {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	states := make([]diagnosticPathState, 0, len(paths))
	for _, path := range paths {
		_, err := os.Stat(path)
		states = append(states, diagnosticPathState{Path: path, Exists: err == nil})
	}
	return states
}
