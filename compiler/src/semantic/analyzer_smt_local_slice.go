package semantic

import (
	"strings"

	"elisacore/src/smtquery"
)

// Keep analyzer call sites stable while query slicing is compiled independently.
func sliceImmutableLocalHypotheses(localHyps, context string) string {
	return smtquery.SliceImmutableLocalHypotheses(localHyps, context)
}
func localHypothesisSymbol(line string) (string, bool) { return smtquery.LocalHypothesisSymbol(line) }

// smtPreambleContext is the part of a fact preamble that can CONNECT symbols: declarations and
// single-symbol bound assertions (`(assert (< v_x 256))`) only constrain one symbol, so they must not
// make every declared local look reachable.
func smtPreambleContext(preamble string) string {
	b := smtTextPool.Get().(*smtTextBuilder)
	defer func() {
		if cap(b.buf) <= smtTextBuilderMaxRetained {
			b.buf = b.buf[:0]
			smtTextPool.Put(b)
		}
	}()
	b.buf = smtquery.AppendPreambleContext(b.buf, preamble)
	return b.String()
}

func smtOperatorOrNumeral(tok string) bool { return smtquery.OperatorOrNumeral(tok) }

// smtTokenScanner preserves smtTokens' FieldsFunc delimiters exactly. Unlike
// smtAtomScanner it does not interpret quotes or split carriage returns.
type smtTokenScanner struct {
	text   string
	offset int
}

func (scan *smtTokenScanner) next() (string, bool) {
	token, offset, ok := smtquery.NextSimpleToken(scan.text, scan.offset)
	scan.offset = offset
	return token, ok
}

// smtTokens collects tokens only when the caller needs a retained work queue.
func smtTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '\n' || r == '\t'
	})
}
