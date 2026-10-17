package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sameSyntaxFiles(a, b *ast.File) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Filename != b.Filename || !reflect.DeepEqual(a.Decls, b.Decls) || len(a.DeclVisibility) != len(b.DeclVisibility) {
		return false
	}
	forward, reverse := map[ast.Node]ast.Node{}, map[ast.Node]ast.Node{}
	var pair func(reflect.Value, reflect.Value) bool
	pair = func(left, right reflect.Value) bool {
		if left.Type() != right.Type() {
			return false
		}
		switch left.Kind() {
		case reflect.Interface:
			if left.IsNil() || right.IsNil() {
				return left.IsNil() == right.IsNil()
			}
			return pair(left.Elem(), right.Elem())
		case reflect.Pointer:
			if left.IsNil() || right.IsNil() {
				return left.IsNil() == right.IsNil()
			}
			if node, ok := left.Interface().(ast.Node); ok {
				other := right.Interface().(ast.Node)
				if seen, ok := forward[node]; ok {
					return seen == other
				}
				if _, ok := reverse[other]; ok {
					return false
				}
				forward[node] = other
				reverse[other] = node
			}
			return pair(left.Elem(), right.Elem())
		case reflect.Struct:
			for i := 0; i < left.NumField(); i++ {
				if !pair(left.Field(i), right.Field(i)) {
					return false
				}
			}
		case reflect.Slice, reflect.Array:
			if left.Len() != right.Len() {
				return false
			}
			for i := 0; i < left.Len(); i++ {
				if !pair(left.Index(i), right.Index(i)) {
					return false
				}
			}
		case reflect.Map:
			if left.Len() != right.Len() {
				return false
			}
			iter := left.MapRange()
			for iter.Next() {
				other := right.MapIndex(iter.Key())
				if !other.IsValid() || !pair(iter.Value(), other) {
					return false
				}
			}
		}
		return true
	}
	if !pair(reflect.ValueOf(a.Decls), reflect.ValueOf(b.Decls)) {
		return false
	}
	for node, visibility := range a.DeclVisibility {
		mapped, ok := forward[node]
		if !ok {
			return false
		}
		other, ok := mapped.(ast.Decl)
		if !ok {
			return false
		}
		value, exists := b.DeclVisibility[other]
		if !exists || value != visibility {
			return false
		}
	}
	return true
}

func TestSyntaxTraceReplaysEditedDeclarationPrefix(t *testing.T) {
	for _, prefix := range []string{
		typestateSocketSrc + "\n",
		"def first():\n    rebind x: i64 = 1\n\n",
		"def first() -> i64:\n    return 1\n\n",
		"extern enum First of i32:\n    Ready = 0\n\n",
		"module Public:\n    public def first() -> i64:\n        return 1\n\n",
		"def first(n: int) -> void:\n    for i in 0..<n by par:\n        pass\n\n",
		"def first():\n    nursery workers(4):\n        pass\n\n",
	} {
		t.Run(strings.Split(prefix, "\n")[0], func(t *testing.T) {
			source := prefix + "def last() -> i64:\n    return 2\n"
			old := lexer.TokenizeSource("trace.elisa", []byte(source), nil)
			p := NewWithSyntaxTrace(old.CopyTokens())
			file := p.ParseFile("trace.elisa")
			if len(p.Errors()) > 0 || len(p.Notices()) > 0 {
				t.Fatalf("bad fixture %v %v", p.Errors(), p.Notices())
			}
			snapshot, err := ast.NewSyntaxSnapshot(file, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			// Insertion changes token count as well as the tail's source positions.
			changed := strings.Replace(source, "return 2", "return 2 + 3", 1)
			tokens := lexer.New("trace.elisa", []byte(changed)).Tokenize()
			trace := p.SyntaxTrace()
			cp, ok := trace.ReusablePrefix(old.MatchingTokenPrefix(tokens), len(tokens))
			if !ok || cp.Declarations() < 1 {
				t.Fatalf("no prefix reuse; trace=%#v", trace)
			}
			prefixFile, err := snapshot.CopyPrefix(cp.Declarations())
			if err != nil {
				t.Fatal(err)
			}
			warm := NewWithSyntaxTrace(tokens)
			got := warm.ParseFileFromPrefix("trace.elisa", prefixFile, trace, cp)
			cold := New(lexer.New("trace.elisa", []byte(changed)).Tokenize())
			want := cold.ParseFile("trace.elisa")
			if !sameSyntaxFiles(got, want) || !reflect.DeepEqual(warm.Errors(), cold.Errors()) || !reflect.DeepEqual(warm.Notices(), cold.Notices()) || warm.rebindCounter != cold.rebindCounter || warm.nurseryCounter != cold.nurseryCounter || warm.byParSeq != cold.byParSeq || warm.foreignEnumErrorSetEmitted != cold.foreignEnumErrorSetEmitted {
				t.Fatal("replayed parse/state differs from cold")
			}
		})
	}
}

func TestSyntaxTraceTracksSpeculationEOFAndRecovery(t *testing.T) {
	p := NewWithSyntaxTrace([]lexer.Token{{Kind: lexer.TOKEN_IDENT}, {Kind: lexer.TOKEN_EOF}})
	p.tokenAtOrLast(0)
	if p.readLength {
		t.Fatal("in-bounds clamped probe depends on whole token length")
	}
	p.tokenAt(1)
	p.seek(0)
	if p.readThrough != 2 {
		t.Fatal("rewinding discarded inspected dependency")
	}
	p.hasToken(2)
	if !p.readLength {
		t.Fatal("failed existence probe omitted")
	}
	p.rewriteTokenKind(0, lexer.TOKEN_IDENT)
	if !p.trace.invalid {
		t.Fatal("token rewrite remained reusable")
	}
}

func TestSyntaxTraceTokenReadChannelsAreClosed(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		allowed := map[string]bool{}
		switch path {
		case "parser_token_access.go":
			for _, name := range strings.Fields("observeToken hasToken tokenAt tokenCount tokenSpan rewriteTokenKind tokenAtOrLast cur peekAt advance seek") {
				allowed[name] = true
			}
		case "parser_estimate.go":
			allowed["estimateIndentedItemsFrom"] = true
			allowed["estimateCommaSeparatedCount"] = true
		case "syntax_trace.go":
			allowed["ParseFileFromPrefix"] = true
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok {
				continue
			}
			goast.Inspect(fn, func(n goast.Node) bool {
				positionWrite := false
				isPos := func(expr goast.Expr) bool { sel, ok := expr.(*goast.SelectorExpr); return ok && sel.Sel.Name == "pos" }
				switch node := n.(type) {
				case *goast.AssignStmt:
					for _, lhs := range node.Lhs {
						positionWrite = positionWrite || isPos(lhs)
					}
				case *goast.IncDecStmt:
					positionWrite = isPos(node.X)
				case *goast.UnaryExpr:
					positionWrite = node.Op == token.AND && isPos(node.X)
				}
				if positionWrite && !(path == "parser_token_access.go" && (fn.Name.Name == "seek" || fn.Name.Name == "advance")) && !(path == "syntax_trace.go" && fn.Name.Name == "ParseFileFromPrefix") {
					t.Errorf("unobserved cursor write at %s", fset.Position(n.Pos()))
				}

				if selector, ok := n.(*goast.SelectorExpr); ok && selector.Sel.Name == "tokens" && !allowed[fn.Name.Name] {
					t.Errorf("untracked parser tokens at %s", fset.Position(selector.Pos()))
				}
				return true
			})
		}
	}
	// A new parser field needs an explicit checkpoint/quiescence audit.
	want := strings.Fields("trace readThrough readLength tokens pos errors notices exprBlockDepth nestDepth rebindCounter poolScopes nurseryGroupByPool nurseryCounter declVisibility currentVisibility allowInMembership allowTernary allowWhereExpr allowQuantifiers disallowIsComparison staticFunctionDepth pendingStmts byParSeq pendingDecls foreignEnumErrorSetEmitted pendingStateDecls stmtGuardArmed stmtGuardCond stmtGuardBlockEnd loopBreakTargets")
	typ := reflect.TypeOf(Parser{})
	if typ.NumField() != len(want) {
		t.Fatal("Parser state changed; audit syntax checkpoint coverage")
	}
	for _, name := range want {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("missing audited field %s", name)
		}
	}
}

func TestSyntaxTraceReplaysForeignEnumOutgoingState(t *testing.T) {
	source := "extern enum First of i32:\n    Ready = 0\n\nextern enum Last of i32:\n    Done = 1\n"
	old := lexer.TokenizeSource("enums.elisa", []byte(source), nil)
	p := NewWithSyntaxTrace(old.CopyTokens())
	file := p.ParseFile("enums.elisa")
	if len(p.Errors()) > 0 {
		t.Fatal(p.Errors())
	}
	snapshot, err := ast.NewSyntaxSnapshot(file, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(source, "Done = 1", "Done = 2", 1)
	tokens := lexer.New("enums.elisa", []byte(changed)).Tokenize()
	cp, ok := p.SyntaxTrace().ReusablePrefix(old.MatchingTokenPrefix(tokens), len(tokens))
	if !ok || cp.Declarations() < 3 {
		t.Fatal("no synthesized declaration prefix")
	}
	prefix, err := snapshot.CopyPrefix(cp.Declarations())
	if err != nil {
		t.Fatal(err)
	}
	warm := NewWithSyntaxTrace(tokens)
	got := warm.ParseFileFromPrefix("enums.elisa", prefix, p.SyntaxTrace(), cp)
	cold := New(lexer.New("enums.elisa", []byte(changed)).Tokenize())
	want := cold.ParseFile("enums.elisa")
	if len(warm.Errors()) > 0 || !sameSyntaxFiles(got, want) {
		t.Fatalf("ForeignEnum error set emitted twice or transition dropped: %v", warm.Errors())
	}
}

func TestSyntaxTraceInvalidatesInspectedNextDeclaration(t *testing.T) {
	source := "def first() -> i64:\n    return 1\n\ndef last() -> i64:\n    return 2\n"
	old := lexer.TokenizeSource("lookahead.elisa", []byte(source), nil)
	p := NewWithSyntaxTrace(old.CopyTokens())
	p.ParseFile("lookahead.elisa")
	changed := strings.Replace(source, "def last() -> i64:\n    return 2", "const last = 2", 1)
	tokens := lexer.New("lookahead.elisa", []byte(changed)).Tokenize()
	if _, ok := p.SyntaxTrace().ReusablePrefix(old.MatchingTokenPrefix(tokens), len(tokens)); ok {
		t.Fatal("checkpoint ignored inspected next declaration token")
	}
}

func TestSyntaxTraceSeededEditParity(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x * 3 + 1\n\n", i)
	}
	original := source.String()
	old := lexer.TokenizeSource("history.elisa", []byte(original), nil)
	p := NewWithSyntaxTrace(old.CopyTokens())
	file := p.ParseFile("history.elisa")
	snapshot, err := ast.NewSyntaxSnapshot(file, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	trace := p.SyntaxTrace()
	rng := rand.New(rand.NewSource(8675309))
	reused := 0
	for iteration := 0; iteration < 160; iteration++ {
		start := rng.Intn(len(original) + 1)
		end := min(len(original), start+rng.Intn(8))
		insert := []string{"", "\n", "(", ")", " + 9", "\npublic ", "'", " # comment\n", "    ", "const "}[rng.Intn(10)]
		changed := original[:start] + insert + original[end:]
		lex := lexer.New("history.elisa", []byte(changed))
		tokens := lex.Tokenize()
		if len(lex.Errors()) > 0 {
			continue
		}
		warm := NewWithSyntaxTrace(append([]lexer.Token(nil), tokens...))
		var got *ast.File
		if cp, ok := trace.ReusablePrefix(old.MatchingTokenPrefix(tokens), len(tokens)); ok {
			prefix, err := snapshot.CopyPrefix(cp.Declarations())
			if err != nil {
				t.Fatal(err)
			}
			got = warm.ParseFileFromPrefix("history.elisa", prefix, trace, cp)
			reused++
		} else {
			got = warm.ParseFile("history.elisa")
		}
		cold := New(append([]lexer.Token(nil), tokens...))
		want := cold.ParseFile("history.elisa")
		if !sameSyntaxFiles(got, want) || !reflect.DeepEqual(warm.Errors(), cold.Errors()) || !reflect.DeepEqual(warm.Notices(), cold.Notices()) {
			t.Fatalf("edit %d differs: start=%d end=%d insert=%q warm=%v cold=%v", iteration, start, end, insert, warm.Errors(), cold.Errors())
		}
	}
	if reused < 20 {
		t.Fatalf("insufficient actual edited-prefix coverage: %d", reused)
	}
}
