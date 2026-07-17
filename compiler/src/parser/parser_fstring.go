package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// f-string desugar (docs: f-string interpolation, Stage A).
//
// The lexer delivers `f"...{EXPR}..."` as ONE TOKEN_FSTRING_LIT whose Text is the raw interior.
// This splits it into literal chunks and embedded expression spans and desugars the whole literal
// into a call to the `__fstr` builtin over the interleaved parts:
//
//	f"a{x}b"  ->  __fstr("a", x, "b")
//
// `__fstr` is not a user-definable function: the semantic analyzer recognizes the call as a builtin
// (string-like parts, result dstr, allocates), and the backend lowers it to one presized allocation
// plus one append per part. Using a plain CallExpr keeps every generic AST consumer (walkers,
// termination analysis, purity, cloning, unparse) working with zero f-string knowledge.
//
// Literal chunks decode the standard string escapes plus `{{`/`}}` for literal braces. Embedded
// expressions are sub-lexed and sub-parsed with fresh Lexer/Parser instances; their diagnostics are
// re-reported against the f-string's position (Stage A: positions point at the literal). Empty
// chunks are dropped (no zero-length appends); an empty interpolation `{}` is an error.
func (p *Parser) desugarFString(tok lexer.Token) ast.Expr {
	raw := tok.Text
	var args []ast.Expr
	chunk := make([]byte, 0, len(raw))
	flushChunk := func() {
		if len(chunk) == 0 {
			return
		}
		args = append(args, &ast.StringLit{Position: tok.Pos, Value: string(chunk)})
		chunk = chunk[:0]
	}

	i := 0
	for i < len(raw) {
		ch := raw[i]
		switch {
		case ch == '{' && i+1 < len(raw) && raw[i+1] == '{':
			chunk = append(chunk, '{')
			i += 2
		case ch == '}' && i+1 < len(raw) && raw[i+1] == '}':
			chunk = append(chunk, '}')
			i += 2
		case ch == '{':
			// Embedded expression: scan to the matching brace, string-aware (mirrors the lexer's scan).
			j := i + 1
			depth := 1
			for j < len(raw) && depth > 0 {
				switch raw[j] {
				case '{':
					depth++
				case '}':
					depth--
				case '"':
					j++
					for j < len(raw) && raw[j] != '"' {
						if raw[j] == '\\' {
							j++
						}
						j++
					}
				}
				j++
			}
			exprSrc := ""
			if depth == 0 {
				exprSrc = raw[i+1 : j-1]
			} else {
				exprSrc = raw[i+1:]
				p.errorAt(tok.Pos, "unterminated `{` in f-string")
				j = len(raw)
			}
			flushChunk()
			if part, ok := p.parseFStringExpr(exprSrc, tok.Pos); ok {
				args = append(args, part)
			}
			i = j
		case ch == '}':
			// A lone `}` is tolerated as literal text (mirrors the lexer's scan).
			chunk = append(chunk, '}')
			i++
		case ch == '\\' && i+1 < len(raw):
			esc := raw[i+1]
			switch esc {
			case 'n':
				chunk = append(chunk, '\n')
			case 'r':
				chunk = append(chunk, '\r')
			case 't':
				chunk = append(chunk, '\t')
			case '0':
				chunk = append(chunk, 0)
			case '\\', '"', '{', '}':
				chunk = append(chunk, esc)
			default:
				p.errorAt(tok.Pos, "invalid escape sequence in f-string literal")
			}
			i += 2
		default:
			chunk = append(chunk, ch)
			i++
		}
	}
	flushChunk()

	// Every f-string lowers through `__fstr`, so `f"…"` has ONE type — the owned
	// formatted string (darray[u8]) — whether or not it interpolates. (A prior
	// optimization returned a bare static-string literal for the no-interpolation case,
	// which typed as `static u8&` and would not unify with interpolated arms in a
	// match/if expression — docs/119 dogfooding gap #3.) An empty `f""` becomes
	// `__fstr("")`.
	if len(args) == 0 {
		args = append(args, &ast.StringLit{Position: tok.Pos, Value: ""})
	}
	return &ast.CallExpr{
		Position: tok.Pos,
		Func:     &ast.Ident{Position: tok.Pos, Name: "__fstr"},
		Args:     args,
	}
}

// parseFStringExpr sub-lexes and sub-parses one embedded `{EXPR}` source span. Errors from the
// sub-lex/sub-parse are re-reported on the enclosing f-string's position so nothing is silently
// swallowed. Returns ok=false for an empty/unparseable span.
func (p *Parser) parseFStringExpr(src string, pos lexer.Pos) (ast.Expr, bool) {
	trimmed := false
	for _, c := range []byte(src) {
		if c != ' ' && c != '\t' {
			trimmed = true
			break
		}
	}
	if !trimmed {
		p.errorAt(pos, "empty `{}` interpolation in f-string")
		return nil, false
	}
	subLexer := lexer.New(pos.File, []byte(src))
	tokens := subLexer.Tokenize()
	for _, e := range subLexer.Errors() {
		p.errorAt(pos, "in f-string interpolation: %s", e)
	}
	// The sub-lexer treats the interpolation text as its OWN source, so its token positions are
	// offsets within that fragment (`{n}` in `f"count {n}"` lexes at line 1, col 1). Rebasing the
	// tokens onto the literal's position gives every node built from them the f-string's position
	// — the Stage A contract stated above, which the errors raised here already follow.
	//
	// Without this only the PARSER's own diagnostics were rebased; the returned expression kept
	// fragment-relative positions, so any LATER diagnostic against it printed a position from the
	// mini-source: `f"count {n}"` on line 3 reported its interpolation type error at `1:1-2`, and a
	// second interpolation at `1:2-3` — the index masquerading as a location.
	for i := range tokens {
		tokens[i].Pos = pos
	}
	sub := New(tokens)
	expr := sub.parseExpr()
	for _, e := range sub.Errors() {
		p.errorAt(pos, "in f-string interpolation: %s", e)
	}
	if expr == nil {
		return nil, false
	}
	return expr, true
}
