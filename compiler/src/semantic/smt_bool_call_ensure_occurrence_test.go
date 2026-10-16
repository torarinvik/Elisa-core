//go:build cgo

package semantic

import "testing"

const smtBoolCallOccurrencePrelude = `
def valid_req(buf: darray[u8]&, start: usize) -> bool:
    ensure result == (buf.count > 0 and start < buf.count)
    return buf.count > 0 and start < buf.count

ghost def valid_req_ghost(buf: darray[u8]&, start: usize) -> bool:
    ensure result == (buf.count > 0 and start < buf.count)
    return buf.count > 0 and start < buf.count
`

// A bool callee's ensures hold at every occurrence of the call in an obligation, not only at
// `return f(x)`: `valid_req(buf, start) implies result == 1` needs `valid_req`'s ensure.
func TestSMTBoolCallEnsureAtAnyOccurrence(t *testing.T) {
	cases := map[string]string{
		"plain_occurrence": `
def plain_occurrence(buf: darray[u8]&, start: usize) -> i64:
    ensure valid_req(buf, start) implies result == 1
    n: usize = buf.count
    return 0 if n == 0
    return 0 if start >= n
    return 1
`,
		"ghost_occurrence": `
def ghost_occurrence(buf: darray[u8]&, start: usize) -> i64:
    ensure valid_req_ghost(buf, start) implies result == 1
    n: usize = buf.count
    return 0 if n == 0
    return 0 if start >= n
    return 1
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtBoolCallOccurrencePrelude+src)
			if n := ensureFailuresFor(result.Errors(), name); n != 0 {
				t.Fatalf("%s: expected the callee ensure to discharge, got: %v", name, result.Errors())
			}
		})
	}
	falseCases := map[string]string{
		"false_occurrence": `
def false_occurrence(buf: darray[u8]&, start: usize) -> i64:
    ensure valid_req(buf, start) implies result == 0
    n: usize = buf.count
    return 0 if n == 0
    return 0 if start >= n
    return 1
`,
		"false_ghost_occurrence": `
def false_ghost_occurrence(buf: darray[u8]&, start: usize) -> i64:
    ensure not valid_req_ghost(buf, start) implies result == 1
    n: usize = buf.count
    return 0 if n == 0
    return 0 if start >= n
    return 1
`,
	}
	for name, src := range falseCases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtBoolCallOccurrencePrelude+src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
