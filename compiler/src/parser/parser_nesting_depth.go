package parser

import (
	"fmt"

	"elisacore/src/lexer"
)

// MaxNestingDepth bounds how deeply expressions, operator chains, blocks and type expressions
// may nest. Every later phase (analysis, constant evaluation, lowering, emission) walks the AST
// recursively, so an unbounded tree depth turns into a Go stack overflow — a fatal runtime
// crash rather than a diagnostic. 20,000 nested parentheses or a 200,000-term `1 + 1 + …` chain
// both did exactly that. One thousand levels is far beyond any program a person or a code
// generator writes (the self-hosted compiler peaks well under a hundred), while the deepest
// phase stays comfortably inside the default goroutine stack at this depth.
const MaxNestingDepth = 1000

// nestingOverflow is the panic value raised when MaxNestingDepth is exceeded. Unwinding by panic
// (recovered in ParseFile) keeps the diagnostic to a single line: the alternative — skipping to
// EOF and returning placeholders — would let every enclosing construct report its own missing
// closer on the way out.
type nestingOverflow struct {
	pos lexer.Pos
}

func (n nestingOverflow) message() string {
	return fmt.Sprintf("%s: nesting too deep: exceeds the maximum depth of %d", n.pos, MaxNestingDepth)
}

// enterNesting records one more level of recursive parsing; pair it with a deferred leaveNesting.
func (p *Parser) enterNesting() {
	p.nestDepth++
	if p.nestDepth > MaxNestingDepth {
		panic(nestingOverflow{pos: p.cur().Pos})
	}
}

func (p *Parser) leaveNesting() {
	p.nestDepth--
}

// checkChainDepth guards the iterative binary-operator and postfix loops: a left-leaning chain
// of n operators is a tree n levels deep for every later recursive walk, even though the parser
// itself builds it in a loop.
func (p *Parser) checkChainDepth(chain int) {
	if p.nestDepth+chain > MaxNestingDepth {
		panic(nestingOverflow{pos: p.cur().Pos})
	}
}

// recoverNestingOverflow converts a nestingOverflow panic into a parse error; any other panic
// propagates unchanged.
func (p *Parser) recoverNestingOverflow() {
	if r := recover(); r != nil {
		overflow, ok := r.(nestingOverflow)
		if !ok {
			panic(r)
		}
		p.errors = append(p.errors, overflow.message())
		p.pos = len(p.tokens) - 1
		if p.pos < 0 {
			p.pos = 0
		}
	}
}
