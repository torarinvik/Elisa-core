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
	// A conservative negative filter avoids lexing an entire include-expanded
	// program when every possible directive names a path already ignored by add.
	// Possible bare names still use the real lexer, including comment/string and
	// directive syntax handling; the filter never extracts a filename itself.
	if mayHaveBareLineFilename(source) {
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

func mayHaveBareLineFilename(source []byte) bool {
	// The lexer consumes attribution only at physical column zero: either its
	// atLineStart path, or handleIndentation with zero measured indentation.
	// In an accepted directive, everything before the filename is '#line',
	// whitespace and decimal digits. Therefore a slash anywhere after '#line'
	// necessarily belongs to the filename, which add excludes. A slash-free
	// candidate (even a malformed one or one inside a block comment) falls back.
	for {
		if bytes.HasPrefix(source, []byte("#line")) {
			end := bytes.IndexByte(source, '\n')
			if end < 0 {
				end = len(source)
			}
			if !bytes.Contains(source[5:end], []byte("/")) {
				return true
			}
			source = source[end:]
		}
		next := bytes.Index(source, []byte("\n#line"))
		if next < 0 {
			return false
		}
		source = source[next+1:]
	}
}
