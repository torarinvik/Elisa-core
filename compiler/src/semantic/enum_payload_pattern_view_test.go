package semantic

import (
	"strings"
	"testing"
)

const enumPayloadPatternPrelude = `enum Tok:
    Word(text: sview)
    Nil

def mk() -> darray[u8]:
    b: mutable darray[u8] = []
    b.push(65.u8())
    return b

`

// A payload view bound by a match/`is` pattern borrows the scrutinee's backing storage: a
// later clear/grow/reassign of that storage must make the binding stale, whether the
// scrutinee is an immutable local (payload resolved to the constructor argument) or a
// mutable local (payload unresolvable; the scrutinee's facts are inherited).
func TestEnumPayloadPatternBindingInvalidatedByOwnerMutation(t *testing.T) {
	cases := map[string]string{
		"match_clear": `def f() -> i64:
    buf: mutable darray[u8] = mk()
    t: Tok = Tok.Word(buf.as_sview())
    match t:
        Tok.Word(text):
            buf.clear()
            return text[0].i64()
        Tok.Nil: return 0
`,
		"is_push": `def f() -> i64:
    buf: mutable darray[u8] = mk()
    t: Tok = Tok.Word(buf.as_sview())
    if t is Tok.Word(text):
        buf.push(1.u8())
        return text[0].i64()
    return 0
`,
		"mutable_scrutinee_reassign": `def f() -> i64:
    buf: mutable darray[u8] = mk()
    t: mutable Tok = Tok.Word(buf.as_sview())
    match t:
        Tok.Word(text):
            buf <- mk()
            return text[0].i64()
        Tok.Nil: return 0
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", enumPayloadPatternPrelude+body)
			all := strings.Join(result.Errors(), "\n")
			if !strings.Contains(all, `view "text" cannot be used: storage dependency facts were invalidated`) {
				t.Fatalf("expected the payload binding to be invalidated, got:\n%s", all)
			}
		})
	}
}

func TestEnumPayloadPatternBindingAcceptsUnrelatedMutation(t *testing.T) {
	cases := map[string]string{
		"used_before_clear": `def f() -> i64:
    buf: mutable darray[u8] = mk()
    t: Tok = Tok.Word(buf.as_sview())
    r: mutable i64 = 0
    match t:
        Tok.Word(text): r <- text[0].i64()
        Tok.Nil: r <- 1
    buf.clear()
    return r
`,
		"param_scrutinee_other_buffer": `def f(t: Tok, buf: mutable darray[u8]&) -> i64:
    match t:
        Tok.Word(text):
            buf.clear()
            return text[0].i64()
        Tok.Nil: return 0
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", enumPayloadPatternPrelude+body)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("expected no errors, got:\n%s", strings.Join(errs, "\n"))
			}
		})
	}
}
