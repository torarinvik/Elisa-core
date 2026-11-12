package retainedsyntax

import (
	"elisacore/src/astscan"
	"regexp"
	"strconv"
	"strings"
)

// The parser names synthesized declarations and auto arenas after their byte
// offset (`__auto_10116619`, `__MachineMode_<hash>_10116619`), so an edit renames
// them in every later declaration of the expansion without changing it.
var Gensym = regexp.MustCompile(`(__[A-Za-z][A-Za-z0-9_]*?)_[0-9]+\b`)

// Auto arenas and runtime quantifier results are bindings local to the body or
// expression block that declares them.
var LocalGensym = regexp.MustCompile(`^__(auto|quantifier)_[0-9]+$`)

var LocalGensymRef = regexp.MustCompile(`\b__(auto|quantifier)_([0-9]+)\b`)

// LocalGensymName recognizes the exact language of LocalGensym
// without paying regexp-engine overhead in reflective source scans. Keep the
// regexp as an independent oracle in tests; this predicate only classifies a
// spelling and does not grant binder authority.
func LocalGensymName(name string) bool {
	return astscan.IsLocalGeneratedName(name)
}

// ShiftLocalGensyms renames the body-local gensyms in text for a body that moved by
// shift bytes.
func ShiftLocalGensyms(text string, shift int) string {
	if shift == 0 || !strings.Contains(text, "__") {
		return text
	}
	return LocalGensymRef.ReplaceAllStringFunc(text, func(name string) string {
		m := LocalGensymRef.FindStringSubmatch(name)
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return name
		}
		return "__" + m[1] + "_" + strconv.Itoa(n+shift)
	})
}

func NormalizeGensyms(text string) string {
	if !strings.Contains(text, "__") {
		return text
	}
	return Gensym.ReplaceAllString(text, "${1}_")
}

// SharedGensyms lists the offset-named identifiers a body mentions that
// may name declarations outside it. A spliced body keeps its new names while the
// retained declarations keep the old ones, so these must not change.
func SharedGensyms(text string) string {
	var out []string
	for _, name := range Gensym.FindAllString(text, -1) {
		if !LocalGensymName(name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}
