package semantic

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// smtAlphaQuery produces an additional, proof-only cache index for a complete
// query. Generated opaque names encode source locations; uniformly renaming
// declared free symbols preserves the formula while avoiding misses after an
// unrelated edit moves those locations. Declaration order, sorts, assertions,
// strings and every other symbol remain significant. Models never use this key.
func smtAlphaQuery(query string) (string, bool) {
	var out strings.Builder
	out.Grow(min(len(query), 4096))
	if !smtAlphaWriteQuery(query, &out) {
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
func smtAlphaWriteQuery(query string, out smtAlphaOutput) bool {
	if len(query) > 8<<20 || !utf8.ValidString(query) {
		return false
	}
	// Do not repair malformed input by dropping a comment containing invalid
	// control bytes. Generated queries use LF; unusual line endings keep exact keys.
	for i := 0; i < len(query); i++ {
		if query[i] < 32 && query[i] != '\n' && query[i] != '\t' {
			return false
		}
	}
	names := make(map[string]string)
	var ordered []string
	const namespace = "__elisa_proof_alpha_"
	prefixExtra := 0
	previous, beforePrevious := "", ""
	ok := smtAlphaWalk(query, func(tok smtAlphaToken, depth int) bool {
		if tok.kind == 'a' {
			id := smtAlphaSymbol(tok.text)
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
		if depth == 1 && beforePrevious == "(" && (previous == "declare-const" || previous == "declare-fun") && tok.kind == 'a' {
			if !smtAlphaValidSymbol(tok.text) {
				return false
			}
			id := smtAlphaSymbol(tok.text)
			if smtAlphaGeneratedSymbol(id) {
				if _, duplicate := names[id]; duplicate {
					return false
				}
				names[id] = ""
				ordered = append(ordered, id)
			}
		}
		beforePrevious, previous = previous, tok.text
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
	ok = smtAlphaWalk(query, func(tok smtAlphaToken, _ int) bool {
		if !first {
			if out.WriteByte(' ') != nil {
				return false
			}
		}
		first = false
		if tok.kind == 'a' {
			if replacement, present := names[smtAlphaSymbol(tok.text)]; present {
				if !smtAlphaValidSymbol(tok.text) {
					return false
				}
				_, err := out.WriteString(replacement)
				return err == nil
			}
		}
		_, err := out.WriteString(tok.text)
		return err == nil
	})
	if !ok {
		return false
	}
	return true

}

// Reject spellings whose replacement could turn a solver syntax error into a
// valid query. Generated identifiers use printable ASCII or a quoted printable
// ASCII spelling; other accepted SMT spellings conservatively keep exact keys.
func smtAlphaValidSymbol(token string) bool {
	quoted := len(token) >= 2 && token[0] == '|' && token[len(token)-1] == '|'
	name := smtAlphaSymbol(token)
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < 32 || c > 126 || c == '|' || c == '\\' {
			return false
		}
		if !quoted && !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("~!@$%^&*_-+=<>.?/", c)) {
			return false
		}
	}
	return true
}

func smtAlphaGeneratedSymbol(name string) bool {
	// These prefixes belong to compiler-generated constants and uninterpreted
	// functions, not SMT operators or sort names. Unrecognized names keep exact keys.
	for _, prefix := range []string{"v_", "aux_", "ghost_uf_", "sview_length_", "bpred_", "@scalar_call_", "@record_field_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func smtAlphaSymbol(token string) string {
	if len(token) >= 2 && token[0] == '|' && token[len(token)-1] == '|' {
		return token[1 : len(token)-1]
	}
	return token
}

type smtAlphaToken struct {
	text string
	kind byte
}

// SMT strings escape a quote by doubling it. Quoted identifiers are symbols,
// not strings. Comments and whitespace are separators; neither changes a query.
// Unsupported or malformed lexical input simply retains the exact cache path.
func smtAlphaWalk(query string, visit func(smtAlphaToken, int) bool) bool {
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
			if !visit(smtAlphaToken{query[start:i], 'p'}, depth) {
				return false
			}
		case ')':
			depth--
			if depth < 0 {
				return false
			}
			i++
			if !visit(smtAlphaToken{query[start:i], 'p'}, depth) {
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
			if !visit(smtAlphaToken{query[start:i], 's'}, depth) {
				return false
			}
		case '|':
			i++
			end := strings.IndexByte(query[i:], '|')
			if end < 0 || strings.IndexByte(query[i:i+end], '\\') >= 0 {
				return false
			}
			i += end + 1
			if !visit(smtAlphaToken{query[start:i], 'a'}, depth) {
				return false
			}
		default:
			for i < len(query) && strings.IndexByte(" \t\r\n();", query[i]) < 0 {
				if query[i] == '"' || query[i] == '|' {
					return false
				}
				i++
			}
			if !visit(smtAlphaToken{query[start:i], 'a'}, depth) {
				return false
			}
		}
	}
	return depth == 0
}

func (c *smtProofCache) alphaKey(query string) (string, bool) {
	if len(query) > 8<<20 {
		return "", false
	}
	hash := sha256.New()
	// Small obligations need only a small buffer; large ones retain bounded scratch.
	out := bufio.NewWriterSize(hash, min(4096, max(256, len(query)+len(c.identity)+32)))
	// Preserve the exact identity and domain separation used by c.key.
	_, _ = io.WriteString(out, c.identity)
	_ = out.WriteByte(0)
	_, _ = out.WriteString("alpha-declared-v1\x00")
	if !smtAlphaWriteQuery(query, out) || out.Flush() != nil {
		return "", false
	}
	return hex.EncodeToString(hash.Sum(nil)), true
}
