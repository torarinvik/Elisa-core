package semantic

import "sync"

// smtTextPool serves callers without an analyzer at hand.
var smtTextPool = sync.Pool{New: func() any { return &smtTextBuilder{} }}

// smtTextBuilder renders SMT text into a reused byte buffer and copies it out once.
// A strings.Builder grows by doubling, so a large query text cost about twice its
// final size in garbage; these buffers are recycled per analyzer instead.
type smtTextBuilder struct {
	buf []byte
}

func (b *smtTextBuilder) WriteString(s string) { b.buf = append(b.buf, s...) }

func (b *smtTextBuilder) WriteByte(c byte) error {
	b.buf = append(b.buf, c)
	return nil
}

func (b *smtTextBuilder) Len() int { return len(b.buf) }

func (b *smtTextBuilder) String() string { return string(b.buf) }

// smtTextBuilderMaxRetained bounds the memory one recycled buffer may keep.
const smtTextBuilderMaxRetained = 64 << 20

func (a *Analyzer) borrowSMTText() *smtTextBuilder {
	if a == nil {
		return &smtTextBuilder{}
	}
	if n := len(a.smtTextFree); n > 0 {
		b := a.smtTextFree[n-1]
		a.smtTextFree = a.smtTextFree[:n-1]
		return b
	}
	return &smtTextBuilder{}
}

func (a *Analyzer) releaseSMTText(b *smtTextBuilder) {
	if a == nil || b == nil || cap(b.buf) > smtTextBuilderMaxRetained {
		return
	}
	b.buf = b.buf[:0]
	a.smtTextFree = append(a.smtTextFree, b)
}
