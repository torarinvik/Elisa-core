//go:build cgo

package semantic

import "testing"

// Char literals, range match arms and literal range membership lower exactly; off-by-one and
// wrong-literal variants stay unproven.
func TestSMTCharRangeClassifiers(t *testing.T) {
	cases := map[string]string{
		"range_match": `
def range_match(character: char) -> bool:
    ensure result == (character in '0'..='9')
    return match character:
        '0'..='9': true
        _: false
`,
		"range_membership": `
def range_membership(character: char) -> bool:
    ensure result == (character in '0'..='9' or character in 'a'..='f')
    return (character >= '0' and character <= '9') or (character >= 'a' and character <= 'f')
`,
		"char_or_pattern": `
def char_or_pattern(character: char) -> bool:
    ensure result == (character == 'e' or character == 'E')
    return match character:
        'e' | 'E': true
        _: false
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if n := ensureFailuresFor(result.Errors(), name); n != 0 {
				t.Fatalf("%s: expected the classifier ensure to prove, got: %v", name, result.Errors())
			}
		})
	}
	falseCases := map[string]string{
		"range_off_by_one": `
def range_off_by_one(character: char) -> bool:
    ensure result == (character in '0'..='8')
    return match character:
        '0'..='9': true
        _: false
`,
		"wrong_char": `
def wrong_char(character: char) -> bool:
    ensure (character == '1') implies not result
    return match character:
        '0'..='9': true
        _: false
`,
	}
	for name, src := range falseCases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
