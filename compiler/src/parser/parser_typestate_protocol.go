package parser

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// parseTypestateDecl parses the `typestate` protocol-declaration sugar (docs/96): the third orthogonal
// axis of Elisa's verification model — sequence typestate. A single declaration names a finite set of
// protocol states plus the legal transitions between them, and DESUGARS onto the EXISTING named-state /
// strict-protocol-balance machinery (a state-bearing `struct T[state …]` + `derive state` discriminant +
// one transition function per `transition`, each `def t(self: mutable T[From]&) ensures self => To`).
//
// Because it lowers onto that proven substrate, illegal operation SEQUENCES become ordinary
// state-mismatch errors at the call site: `established(sock)` on a `Socket[Closed]` is rejected exactly
// as `def f(x: T[A]&)` rejects a `T[B]` argument. No new prover; a typestate fact is the named-state
// discharge class, composed with value-laws and effect-capabilities by conjunction (docs/85 honesty
// layer), never laundered across classes.
//
// Surface:
//
//	typestate Socket:
//	    fd: mutable i64                       # ordinary data fields (optional)
//	    states: Closed, Connecting, Connected # the finite state set (first listed is the initial state)
//	    transition connect:     Closed     -> Connecting
//	    transition established:  Connecting -> Connected
//	    transition close:        Connected  -> Closed
//
// The desugaring is performed by GENERATING the equivalent Elisa source and re-parsing it, so the
// expansion is guaranteed to be well-formed input to the rest of the pipeline (the same trick the
// grammar DSL uses): one StructDecl is returned, the transition functions are queued in pendingDecls
// and drained flat at file scope by ParseFile.
func (p *Parser) parseTypestateDecl() ast.Decl {
	affinePrefix := ""
	if p.peekIdentText("linear") || p.peekIdentText("affine") {
		affinePrefix = p.advance().Text
	}
	pos := p.cur().Pos
	p.expectIdentText("typestate")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	type transitionDecl struct {
		name string
		from string
		to   string
	}

	var states []string
	var fields []string // reconstructed `name: type` field source lines
	var fieldNames []string
	var transitions []transitionDecl
	seenStates := map[string]bool{}

	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("states"):
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			for {
				st := p.expect(lexer.TOKEN_IDENT).Text
				if seenStates[st] {
					p.errorf("typestate %q declares duplicate state %q", name, st)
				}
				seenStates[st] = true
				states = append(states, st)
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expectNewline()
		case p.peekIdentText("transition"):
			p.advance()
			tname := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_COLON)
			from := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_ARROW)
			to := p.expect(lexer.TOKEN_IDENT).Text
			p.expectNewline()
			transitions = append(transitions, transitionDecl{name: tname, from: from, to: to})
		default:
			// An ordinary data field: `name: type`. Reconstruct the source line from token texts up to
			// the end-of-line so the desugared struct carries the field verbatim.
			line := p.captureFieldLineSource()
			if line != "" {
				fields = append(fields, line)
				if fieldName := typestateFieldName(line); fieldName != "" {
					fieldNames = append(fieldNames, fieldName)
				}
			}
		}
	}
	p.expect(lexer.TOKEN_DEDENT)

	if len(states) < 2 {
		p.errorf("typestate %q must declare at least two states (got %d)", name, len(states))
		return nil
	}
	stateIndex := map[string]int{}
	for i, st := range states {
		stateIndex[st] = i
	}
	for _, tr := range transitions {
		if _, ok := stateIndex[tr.from]; !ok {
			p.errorf("typestate %q transition %q has unknown source state %q", name, tr.name, tr.from)
		}
		if _, ok := stateIndex[tr.to]; !ok {
			p.errorf("typestate %q transition %q has unknown target state %q", name, tr.name, tr.to)
		}
	}

	// Build the desugared source.
	var b strings.Builder
	if affinePrefix != "" {
		fmt.Fprintf(&b, "%s ", affinePrefix)
	}
	fmt.Fprintf(&b, "struct %s[state %s]:\n", name, strings.Join(states, " | "))
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%s\n", f)
	}
	b.WriteString("\t__typestate: mutable i64\n\n")
	b.WriteString("\tderive state:\n")
	for i, st := range states {
		fmt.Fprintf(&b, "\t\t%s when self.__typestate == %d\n", st, i)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "def %s(%s) -> %s[%s]:\n", typestateConstructorFunctionName(name), strings.Join(fields, ", "), name, states[0])
	b.WriteString("\treturn ")
	fmt.Fprintf(&b, "%s{", name)
	for i, fieldName := range fieldNames {
		if i != 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %s", fieldName, fieldName)
	}
	if len(fieldNames) != 0 {
		b.WriteString(", ")
	}
	b.WriteString("__typestate: 0}\n\n")
	for _, tr := range transitions {
		fmt.Fprintf(&b, "def %s(self: mutable %s[%s]&) ensures self => %s:\n", tr.name, name, tr.from, tr.to)
		fmt.Fprintf(&b, "\tself.__typestate <- %d\n\n", stateIndex[tr.to])
	}

	decls := p.reparseDesugaredDecls(name, b.String(), pos)
	if len(decls) == 0 {
		return nil
	}
	// The first decl is the state-bearing struct; the rest (transition functions) are queued so they
	// land flat at file scope alongside it.
	if len(decls) > 1 {
		p.pendingDecls = append(p.pendingDecls, decls[1:]...)
	}
	return decls[0]
}

func typestateConstructorFunctionName(name string) string {
	return "__typestate_" + name + "_new"
}

func typestateFieldName(line string) string {
	beforeColon, _, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(beforeColon)
}

// captureFieldLineSource reconstructs a `name: type[…]` field declaration line from raw tokens up to the
// end of the logical line, so it can be re-emitted verbatim into the desugared struct body.
func (p *Parser) captureFieldLineSource() string {
	var parts []string
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_SEMICOLON {
		parts = append(parts, p.advance().Text)
	}
	if p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_SEMICOLON {
		p.advance()
	}
	return strings.Join(parts, " ")
}

// reparseDesugaredDecls lexes and parses generated Elisa source for the typestate desugaring and returns
// its top-level declarations. Any parse error in the GENERATED source is a compiler bug in the
// desugaring, so it is surfaced against the original `typestate` position rather than swallowed.
func (p *Parser) reparseDesugaredDecls(name, src string, pos lexer.Pos) []ast.Decl {
	l := lexer.New("<typestate:"+name+">", []byte(src))
	tokens := l.Tokenize()
	sub := New(tokens)
	file := sub.ParseFile("<typestate:" + name + ">")
	if len(sub.errors) > 0 {
		p.errorf("internal: typestate %q desugaring failed to parse: %s", name, strings.Join(sub.errors, "; "))
		return nil
	}
	return file.Decls
}
