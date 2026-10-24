package main

import (
	"elisacore/src/lexer"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The preceding implementation is the differential oracle: every possible
// attribution is interpreted by the complete ordinary lexer.
func diagnosticPathInputsLexed(filename string, source []byte) []diagnosticPathState {
	names := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "<unknown>" && !strings.Contains(filepath.ToSlash(name), "/") {
			names[name] = true
		}
	}
	add(filename)
	scanner := lexer.New(filename, source)
	for {
		token := scanner.NextToken()
		add(token.Pos.File)
		if token.Kind == lexer.TOKEN_EOF {
			break
		}
	}
	paths := make([]string, 0, len(names))
	for name := range names {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	states := make([]diagnosticPathState, 0, len(paths))
	for _, name := range paths {
		_, err := os.Stat(name)
		states = append(states, diagnosticPathState{Path: name, Exists: err == nil})
	}
	return states
}

func TestDiagnosticPathPrefilterMatchesLexer(t *testing.T) {
	pieces := []string{
		"", "\n", "\r\n", "pass\n", "(\n", ")\n", "\"\"\"", "\"\"\"\n", "\"text\"\n",
		"#line 1 /absolute/source.elisa\n", "#line 1 relative/path.elisa\n",
		"#line 2 bare.elisa\n", "#line\t1\tbare.elisa\t\r\n", "#line 1bare.elisa\n",
		"#line 99999999999999999999999999999999999999 bare.elisa\n",
		"#line 1 <unknown>\n", "#line 1 \n", "#line -1 bare.elisa\n", "#line 1 bare.elisa /suffix\n",
		"#line 1", "#line", "#line/1 bare.elisa\n", "#line 1 bare.elisa\x00\n",
		"#line 1 windows\\source.elisa\n", "#line 1 unicode∕source.elisa\n",
		"#line 1 bare#filename.elisa\n", "# line 1 bare.elisa\n", "#   line 1: text\n",
		" #line 1 bare.elisa\n", "\t#line 1 bare.elisa\n", "x #line 1 bare.elisa\n",
		"\"#line 1 bare.elisa\"\n", "'x' #line 1 bare.elisa\n",
		"\"\"\"\n#line 1 bare.elisa\n\"\"\"\n",
		"\"\"\" hidden \"\"\" #line 1 bare.elisa\n",
		"\"\"\" hidden\n\"\"\" #line 1 bare.elisa\n",
		"a and\n    #line 1 bare.elisa\n", "\\\n#line 1 bare.elisa\n",
	}
	check := func(text string) {
		t.Helper()
		for _, filename := range []string{"/virtual/unit.elisa", "request.elisa"} {
			got := diagnosticPathInputs(filename, []byte(text))
			want := diagnosticPathInputsLexed(filename, []byte(text))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("filename=%q text=%q\ngot=%v want=%v", filename, text, got, want)
			}
		}
	}
	for _, a := range pieces {
		for _, b := range pieces {
			check(a + b)
		}
	}
	rng := rand.New(rand.NewSource(117))
	for i := 0; i < 5000; i++ {
		var text strings.Builder
		for j := 0; j < 1+rng.Intn(20); j++ {
			text.WriteString(pieces[rng.Intn(len(pieces))])
		}
		check(text.String())
	}
}

func TestDiagnosticPathPrefilterTracksBareExistence(t *testing.T) {
	f, err := os.CreateTemp(".", "path-prefilter-*.elisa")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(f.Name())
	f.Close()
	os.Remove(name)
	t.Cleanup(func() { os.Remove(name) })
	source := []byte("#line 1 /absolute/included.elisa\npass\n#line 3 " + name + "\npass\n")
	if !mayHaveBareLineFilename(source) {
		t.Fatal("bare attribution bypassed lexer")
	}
	check := func(exists bool) {
		t.Helper()
		got := diagnosticPathInputs("/virtual/request.elisa", source)
		want := []diagnosticPathState{{Path: name, Exists: exists}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
	check(false)
	if err := os.WriteFile(name, []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	check(false)
	if mayHaveBareLineFilename([]byte("#line 1 /absolute/source.elisa\npass\n")) {
		t.Fatal("absolute include requires full tokenization")
	}
}

func BenchmarkDiagnosticPathInputsExpanded(b *testing.B) {
	filename := os.Getenv("ELISACORE_SOURCE_HANDOFF_PROFILE_INPUT")
	if filename == "" {
		b.Skip("set ELISACORE_SOURCE_HANDOFF_PROFILE_INPUT")
	}
	program, ok := loadProgramInput(filename, io.Discard)
	if !ok {
		b.Fatal("load failed")
	}
	if !reflect.DeepEqual(diagnosticPathInputs(filename, program.source), diagnosticPathInputsLexed(filename, program.source)) {
		b.Fatal("real source path states changed")
	}
	for _, mode := range []struct {
		name string
		run  func(string, []byte) []diagnosticPathState
	}{{"lexed", diagnosticPathInputsLexed}, {"prefilter", diagnosticPathInputs}} {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mode.run(filename, program.source)
			}
		})
	}
}

func FuzzDiagnosticPathPrefilter(f *testing.F) {
	for _, source := range []string{
		"#line 1 /absolute/source.elisa\npass\n",
		"#line 1 bare.elisa\npass\n",
		"\"\"\"\n#line 1 bare.elisa\n\"\"\"\n",
		"(\n#line 99999999999999999999 bare.elisa\n)\n",
		"\"\"\" hidden \"\"\" #line 1 bare.elisa\n",
		"a and\n    #line 1 bare.elisa\n",
	} {
		f.Add([]byte(source))
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > 8192 {
			t.Skip()
		}
		got := diagnosticPathInputs("/virtual/request.elisa", source)
		want := diagnosticPathInputsLexed("/virtual/request.elisa", source)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("source=%q got=%v want=%v", source, got, want)
		}
	})
}
