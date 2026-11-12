package semantic

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"elisacore/src/smtalpha"
)

type smtAlphaOutput interface {
	WriteString(string) (int, error)
	WriteByte(byte) error
}
type smtAlphaToken struct {
	text string
	kind byte
}

func smtAlphaQuery(query string) (string, bool) { return smtalpha.Query(query) }
func smtAlphaWriteQuery(query string, out smtAlphaOutput) bool {
	return smtalpha.WriteQuery(query, out)
}
func smtAlphaHasInvalidControl(query string) bool { return smtalpha.HasInvalidControl(query) }
func smtAlphaValidSymbol(token string) bool       { return smtalpha.ValidSymbol(token) }
func smtAlphaSymbol(token string) string          { return smtalpha.Symbol(token) }
func smtAlphaWalk(query string, visit func(smtAlphaToken, int) bool) bool {
	return smtalpha.Walk(query, func(token smtalpha.Token, depth int) bool {
		return visit(smtAlphaToken{token.Text, token.Kind}, depth)
	})
}

func (c *smtProofCache) alphaKey(query string) (string, bool) {
	if len(query) > 8<<20 {
		return "", false
	}
	hash := sha256.New()
	// Small obligations need only a small buffer; large ones retain bounded scratch.
	out := bufio.NewWriterSize(hash, min(4096, max(256, len(query)+len(c.proofID())+32)))
	// Preserve the exact identity and domain separation used by c.key.
	_, _ = io.WriteString(out, c.proofID())
	_ = out.WriteByte(0)
	_, _ = out.WriteString("alpha-declared-v1\x00")
	if !smtAlphaWriteQuery(query, out) || out.Flush() != nil {
		return "", false
	}
	return hex.EncodeToString(hash.Sum(nil)), true
}
