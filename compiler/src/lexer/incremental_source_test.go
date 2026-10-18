package lexer

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func assertSourceTokensCold(t *testing.T, source string, previous *SourceTokens) *SourceTokens {
	t.Helper()
	current := TokenizeSource("edits.elisa", []byte(source), previous)
	cold := New("edits.elisa", []byte(source))
	tokens := cold.Tokenize()
	if !reflect.DeepEqual(current.CopyTokens(), tokens) || !reflect.DeepEqual(current.CopyErrors(), cold.Errors()) {
		t.Fatalf("incremental lex differs (reused %d):\n%s\nerrors %v / %v", current.reused, source, current.errors, cold.Errors())
	}
	return current
}
func editPreamble() string {
	var out strings.Builder
	for i := 0; i < 160; i++ {
		fmt.Fprintf(&out, "const prefix%d = %d\n", i, i)
	}
	return out.String()
}

func TestSourceTokensEditHistories(t *testing.T) {
	preamble := editPreamble()
	for _, body := range []string{
		"def f(x: i64) -> i64:\n    return x + 21\n",
		"#line 70 original.elisa\ndef f():\n    if true:\n        x = 21\n    pass\n",
		"const s = \"hello\\n\\u03bb\"\nconst c = '\\n'\nconst f = f\"hello {21}\"\n",
		"\"\"\" multiline\ncomment \"\"\"\nconst x = 21\n",
		"const value = (when x:\n    21 => 2\n    _ => 3\n)\n",
		"const value = (do:\n    x: i64 = 21\n    x\n)\n",
	} {
		var previous *SourceTokens
		original := preamble + body
		edits := []string{original, preamble + strings.Replace(body, "21", "422", 1), original + "\nconst appended = 1\n", original[:len(original)-1], original, strings.Replace(original, "prefix0", "renamed", 1), original}
		reused := 0
		for _, source := range edits {
			previous = assertSourceTokensCold(t, source, previous)
			reused += previous.reused
		}
		if reused == 0 {
			t.Fatal("history never reused a prefix")
		}
	}
}

func TestSourceTokensFarLookaheadAndEOF(t *testing.T) {
	prefix := strings.Repeat("x ", 510)
	sources := []string{
		prefix + "(when" + strings.Repeat("\n# lookahead\n", 80) + "value:\n    _ => 1\n)\n",
		prefix + "(when" + strings.Repeat("\n# lookahead\n", 80) + "return:\n    _ => 1\n)\n",
		prefix + "x\n" + strings.Repeat(" ", 400) + "value\n",
		prefix + "x\n" + strings.Repeat(" ", 400) + "and y\n",
		prefix + "(when\n# incomplete",
		prefix + "(when\n# incomplete\nvalue:\n    _ => 1\n)\n",
		prefix + string([]byte{0xf0}),
		prefix + "😀\n",
	}
	for i := 0; i < len(sources); i += 2 {
		before := assertSourceTokensCold(t, sources[i], nil)
		after := assertSourceTokensCold(t, sources[i+1], before)
		if after.reused >= 512 {
			t.Fatalf("lookahead-dependent boundary reused: %d", after.reused)
		}
	}
}

func TestSourceTokensRandomEditParity(t *testing.T) {
	r := rand.New(rand.NewSource(19))
	source := editPreamble() + "def f():\n    x = (do:\n        value: i64 = 1\n        value\n    )\n"
	previous := assertSourceTokensCold(t, source, nil)
	insertions := []string{"", "\n", " ", "# comment\n", "\"", "'", "(", ")", "when", "and", "λ", string([]byte{0xff}), "\"\"\""}
	for i := 0; i < 400; i++ {
		start := r.Intn(len(source) + 1)
		end := min(len(source), start+r.Intn(8))
		source = source[:start] + insertions[r.Intn(len(insertions))] + source[end:]
		previous = assertSourceTokensCold(t, source, previous)
	}
}

func TestSourceTokensRebindAllPriorBackingViews(t *testing.T) {
	original := "const escaped = \"line\\n\\u03bb\"\nconst ch = '\\n'\nconst number = 21u64\n" + editPreamble()
	previous := TokenizeSource("edits.elisa", []byte(original), nil)
	changed := original + "const tail_value = 99\n"
	current := assertSourceTokensCold(t, changed, previous)
	if current.reused < 30 {
		t.Fatal("special decoded/source-view tokens were not inside reused prefix")
	}
	for _, c := range current.checkpoints {
		if len(c.state.src) > 0 && &c.state.src[0] != &current.source[0] {
			t.Fatal("checkpoint retained old source owner")
		}
	}
	for i := range previous.source {
		previous.source[i] = '?'
	}
	cold := New("edits.elisa", []byte(changed)).Tokenize()
	if !reflect.DeepEqual(current.CopyTokens(), cold) {
		t.Fatal("new revision retained prior source views")
	}
}

func TestSourceTokensBudgetCountsCapacity(t *testing.T) {
	source := strings.Repeat("# padding\n", 4096)
	result := TokenizeSource("comments.elisa", []byte(source), nil)
	size, ok := result.RetainedBytes(1 << 30)
	minimum := cap(result.source) + cap(result.tokens)*int(unsafe.Sizeof(Token{})) + cap(result.checkpoints)*int(unsafe.Sizeof(sourceCheckpoint{}))
	if !ok || size < minimum {
		t.Fatalf("retained capacities undercounted: %d < %d", size, minimum)
	}
	if result.checkpointBytes > sourceCheckpointBytes || len(result.checkpoints) > sourceCheckpointLimit {
		t.Fatal("checkpoint budget exceeded")
	}
	// A deep layout must stop capturing checkpoints before duplicating too much
	// stack storage, while the lexer itself still returns the full token stream.
	var deep strings.Builder
	for i := 0; i < 1800; i++ {
		deep.WriteString(strings.Repeat(" ", i))
		deep.WriteString("x\n")
	}
	nested := TokenizeSource("nested.elisa", []byte(deep.String()), nil)
	if nested.checkpointBytes > sourceCheckpointBytes {
		t.Fatal("nested checkpoint storage exceeded cap")
	}
}

func TestSourceCheckpointLexerFieldInventory(t *testing.T) {
	expected := []string{"trackReads", "readLimit", "lineEpoch", "src", "filename", "pos", "line", "col", "indentStack", "atLineStart", "pendingToks", "lastTokenKind", "parenDepth", "pendingGroupedBlock", "pendingPatternBlocks", "groupedBlockContexts", "errors"}
	typ := reflect.TypeOf(Lexer{})
	var actual []string
	for i := 0; i < typ.NumField(); i++ {
		actual = append(actual, typ.Field(i).Name)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("review new lexer state for checkpoint ownership: %v", actual)
	}
}

func TestSourceSensitiveReadsUseTrackingHelpers(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "source_reads.go" || name == "incremental_source.go" || name == "incremental_suffix.go" {
			continue
		}
		file, err := goparser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok {
				continue
			}
			goast.Inspect(fn.Body, func(node goast.Node) bool {
				sel, ok := node.(*goast.SelectorExpr)
				if ok && sel.Sel.Name == "src" && fn.Name.Name != "Tokenize" {
					t.Errorf("untracked source access in %s:%s", name, fn.Name.Name)
				}
				return true
			})
		}
	}
}

func TestSourceTokensIncompleteRuneCheckpoint(t *testing.T) {
	prefix := strings.Repeat("x ", 511)
	old := assertSourceTokensCold(t, prefix+string([]byte{0xf0}), nil)
	current := assertSourceTokensCold(t, prefix+"😀\n", old)
	if current.reused >= 512 {
		t.Fatal("checkpoint depending on incomplete UTF-8 reused")
	}
}

func TestSourceCheckpointBudgetIncludesText(t *testing.T) {
	state := *New("", nil)
	state.filename = strings.Repeat("p", 4096)
	if _, ok := checkpointStorageBytes(state, 4096); ok {
		t.Fatal("checkpoint filename omitted from budget")
	}
	state.filename = ""
	state.errors = []string{strings.Repeat("e", 4096)}
	if _, ok := checkpointStorageBytes(state, 4096); ok {
		t.Fatal("checkpoint diagnostic text omitted from budget")
	}
}
