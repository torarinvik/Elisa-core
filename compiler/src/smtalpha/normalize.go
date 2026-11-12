package smtalpha

import (
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Query produces an additional, proof-only cache index for a complete
// query. Generated opaque names encode source locations; uniformly renaming
// declared free symbols preserves the formula while avoiding misses after an
// unrelated edit moves those locations. Declaration order, sorts, assertions,
// strings and every other symbol remain significant. Models never use this key.
func Query(query string) (string, bool) {
	var out strings.Builder
	out.Grow(min(len(query), 4096))
	if !WriteQuery(query, &out) {
		return "", false
	}
	return out.String(), true
}

type smtAlphaOutput interface {
	WriteString(string) (int, error)
	WriteByte(byte) error
}

// Keep declaration discovery and token normalization identical for text consumers
// and the proof-cache digest, without materializing large normalized queries there.
func WriteQuery(query string, out smtAlphaOutput) bool {
	if len(query) > 8<<20 || !utf8.ValidString(query) {
		return false
	}
	// Do not repair malformed input by dropping a comment containing invalid
	// control bytes. Generated queries use LF; unusual line endings keep exact keys.
	if HasInvalidControl(query) {
		return false
	}
	names := make(map[string]string)
	var ordered []string
	const namespace = "__elisa_proof_alpha_"
	prefixExtra := 0
	previous, beforePrevious := "", ""
	ok := Walk(query, func(tok Token, depth int) bool {
		if tok.Kind == 'a' {
			id := Symbol(tok.Text)
			if strings.HasPrefix(id, namespace) {
				extra := 0
				for i := len(namespace); i < len(id) && id[i] == '_'; i++ {
					extra++
				}
				if extra+1 > prefixExtra {
					prefixExtra = extra + 1
				}
			}
		}
		if depth == 1 && beforePrevious == "(" && (previous == "declare-const" || previous == "declare-fun") && tok.Kind == 'a' {
			if !ValidSymbol(tok.Text) {
				return false
			}
			id := Symbol(tok.Text)
			if smtAlphaGeneratedSymbol(id) {
				if _, duplicate := names[id]; duplicate {
					return false
				}
				names[id] = ""
				ordered = append(ordered, id)
			}
		}
		beforePrevious, previous = previous, tok.Text
		return true
	})
	if !ok || len(ordered) == 0 {
		return false
	}
	// Choose the same absent namespace as the token-vector implementation,
	// while retaining only declarations rather than every occurrence in a VC.
	prefix := namespace + strings.Repeat("_", prefixExtra)
	for i, name := range ordered {
		names[name] = "|" + prefix + strconv.Itoa(i) + "|"
	}
	first := true
	// Only identical token spellings share validation; quoted and unquoted forms
	// can represent the same name while having different validity rules.
	lastValidated := ""
	lastReplacement := ""
	ok = Walk(query, func(tok Token, _ int) bool {
		if !first {
			if out.WriteByte(' ') != nil {
				return false
			}
		}
		first = false
		if tok.Kind == 'a' {
			// Generated names often recur throughout one proof. Reuse the
			// last successful lookup before hashing a potentially long name.
			if tok.Text == lastValidated && lastReplacement != "" {
				_, err := out.WriteString(lastReplacement)
				return err == nil
			}
			id := Symbol(tok.Text)
			// The names map contains only compiler-generated identifiers. Most
			// atoms are operators, keywords, numerals or sorts and cannot match.
			if smtAlphaPossibleGenerated(id) {
				if replacement, present := names[id]; present {
					if tok.Text != lastValidated {
						if !ValidSymbol(tok.Text) {
							return false
						}
						lastValidated = tok.Text
					}
					lastReplacement = replacement
					_, err := out.WriteString(replacement)
					return err == nil
				}
			}
		}
		_, err := out.WriteString(tok.Text)
		return err == nil
	})
	if !ok {
		return false
	}
	return true

}

// Check eight bytes at once in the common case of printable SMT text. Only
// words containing a control byte need the slower byte-by-byte inspection.
func HasInvalidControl(query string) bool {
	i := 0
	for ; i+8 <= len(query); i += 8 {
		word := binary.LittleEndian.Uint64([]byte(query[i:i+8])) & 0xe0e0e0e0e0e0e0e0
		if (word-0x0101010101010101)&^word&0x8080808080808080 == 0 {
			continue
		}
		for j := i; j < i+8; j++ {
			if query[j] < 32 && query[j] != '\n' && query[j] != '\t' {
				return true
			}
		}
	}
	for ; i < len(query); i++ {
		if query[i] < 32 && query[i] != '\n' && query[i] != '\t' {
			return true
		}
	}
	return false
}

// Reject spellings whose replacement could turn a solver syntax error into a
// valid query. Generated identifiers use printable ASCII or a quoted printable
// ASCII spelling; other accepted SMT spellings conservatively keep exact keys.
func ValidSymbol(token string) bool {
	quoted := len(token) >= 2 && token[0] == '|' && token[len(token)-1] == '|'
	name := Symbol(token)
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 32 || c > 126 || c == '|' || c == '\\' {
			return false
		}
		if !quoted && !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("~!@$%^&*_-+=<>.?/", c) >= 0) {
			return false
		}
	}
	return true
}

func smtAlphaGeneratedSymbol(name string) bool {
	// These prefixes belong to compiler-generated constants and uninterpreted
	// functions, not SMT operators or sort names. Unrecognized names keep exact keys.
	if !smtAlphaPossibleGenerated(name) {
		return false
	}
	for _, prefix := range []string{"v_", "aux_", "ghost_uf_", "sview_length_", "bpred_", "@scalar_call_", "@record_field_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func smtAlphaPossibleGenerated(name string) bool {
	return len(name) != 0 && (name[0] == 'v' || name[0] == 'a' || name[0] == 'g' || name[0] == 's' || name[0] == 'b' || name[0] == '@')
}

func Symbol(token string) string {
	if len(token) >= 2 && token[0] == '|' && token[len(token)-1] == '|' {
		return token[1 : len(token)-1]
	}
	return token
}

type Token struct {
	Text string
	Kind byte
}

var smtAlphaAtomEnd = [256]bool{' ': true, '\t': true, '\r': true, '\n': true, '(': true, ')': true, ';': true, '"': true, '|': true}

// SMT strings escape a quote by doubling it. Quoted identifiers are symbols,
// not strings. Comments and whitespace are separators; neither changes a query.
// Unsupported or malformed lexical input simply retains the exact cache path.
func Walk(query string, visit func(Token, int) bool) bool {
	depth := 0
	for i := 0; i < len(query); {
		start := i
		switch query[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case ';':
			for i < len(query) && query[i] != '\n' {
				i++
			}
			continue
		case '(':
			depth++
			if depth > 4096 {
				return false
			}
			i++
			if !visit(Token{query[start:i], 'p'}, depth) {
				return false
			}
		case ')':
			depth--
			if depth < 0 {
				return false
			}
			i++
			if !visit(Token{query[start:i], 'p'}, depth) {
				return false
			}
		case '"':
			i++
			closed := false
			for i < len(query) {
				if query[i] != '"' {
					i++
					continue
				}
				if i+1 < len(query) && query[i+1] == '"' {
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			if !closed {
				return false
			}
			if !visit(Token{query[start:i], 's'}, depth) {
				return false
			}
		case '|':
			i++
			end := strings.IndexByte(query[i:], '|')
			if end < 0 || strings.IndexByte(query[i:i+end], '\\') >= 0 {
				return false
			}
			i += end + 1
			if !visit(Token{query[start:i], 'a'}, depth) {
				return false
			}
		default:
			for i < len(query) && !smtAlphaAtomEnd[query[i]] {
				i++
			}
			if i < len(query) && (query[i] == '"' || query[i] == '|') {
				return false
			}
			if !visit(Token{query[start:i], 'a'}, depth) {
				return false
			}
		}
	}
	return depth == 0
}
