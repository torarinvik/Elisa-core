package semantic

import (
	"unicode/utf8"

	"elisacore/src/ast"
)

func (a *Analyzer) analyzeCharsetDecl(decl *ast.CharsetDecl) {
	if decl == nil {
		return
	}
	seen := map[rune]ast.LexerCharClassTerm{}
	a.collectCharsetTermChars(decl, decl.Terms, seen, map[*ast.CharsetDecl]bool{decl: true})
}

func (a *Analyzer) collectCharsetTermChars(owner *ast.CharsetDecl, terms []ast.LexerCharClassTerm, seen map[rune]ast.LexerCharClassTerm, visiting map[*ast.CharsetDecl]bool) {
	for _, term := range terms {
		if term.Ref {
			ref, ok := a.lookupCharsetRef(owner, term)
			if !ok {
				continue
			}
			if visiting[ref] {
				a.errorf(term.Position, "charset %q contains a reference cycle through %q", owner.Name, term.Name)
				continue
			}
			visiting[ref] = true
			a.collectCharsetTermChars(owner, ref.Terms, seen, visiting)
			delete(visiting, ref)
			continue
		}
		start, ok := a.charsetRune(owner, term, term.Start, "start")
		if !ok {
			continue
		}
		end := start
		if term.Range {
			var endOK bool
			end, endOK = a.charsetRune(owner, term, term.End, "end")
			if !endOK {
				continue
			}
			if start > end {
				a.errorf(term.Position, "charset %q range start %q is greater than end %q", owner.Name, string(start), string(end))
				continue
			}
		}
		for ch := start; ch <= end; ch++ {
			if prev, exists := seen[ch]; exists {
				a.errorf(term.Position, "charset %q contains duplicate character %q (first seen at %s:%d:%d)", owner.Name, string(ch), prev.Position.File, prev.Position.Line, prev.Position.Col)
				continue
			}
			seen[ch] = term
		}
	}
}

func (a *Analyzer) lookupCharsetRef(owner *ast.CharsetDecl, term ast.LexerCharClassTerm) (*ast.CharsetDecl, bool) {
	sym, _, ok := a.lookupVisibleGlobal(term.Name)
	if !ok || sym == nil {
		a.errorf(term.Position, "unknown charset %q referenced by charset %q", term.Name, owner.Name)
		return nil, false
	}
	ref, ok := sym.Node.(*ast.CharsetDecl)
	if !ok || ref == nil {
		a.errorf(term.Position, "charset %q reference %q does not name a charset", owner.Name, term.Name)
		return nil, false
	}
	return ref, true
}

func (a *Analyzer) charsetRune(decl *ast.CharsetDecl, term ast.LexerCharClassTerm, value string, label string) (rune, bool) {
	if utf8.RuneCountInString(value) != 1 {
		a.errorf(term.Position, "charset %q %s literal must decode to exactly one character", decl.Name, label)
		return 0, false
	}
	ch, _ := utf8.DecodeRuneInString(value)
	if ch > 0x7f {
		a.errorf(term.Position, "charset %q %s literal %q is not ASCII; unicode charsets are reserved for a later slice", decl.Name, label, value)
		return 0, false
	}
	return ch, true
}
