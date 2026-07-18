package lexer

import (
	"os"
	"sync"
)

// The internal-parity recorder (ELISA_SEMANTIC_INTERNAL_PARITY_OUT) needs the raw
// source text of every analyzed file, but semantic.AnalyzeWithOptions only sees the
// AST. New() registers each lexed source here, keyed by filename, so the recorder
// can join AST → source. Inert (no allocation, one atomic-free bool check) unless
// the environment variable is set.
var (
	paritySourceMu      sync.Mutex
	paritySources       map[string]string
	parityRecordEnabled = os.Getenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT") != ""
)

func recordParitySource(filename string, src []byte) {
	if !parityRecordEnabled {
		return
	}
	paritySourceMu.Lock()
	defer paritySourceMu.Unlock()
	if paritySources == nil {
		paritySources = make(map[string]string)
	}
	paritySources[filename] = string(src)
}

// ParitySourceFor returns the most recently lexed source registered under
// filename (test helpers reuse filenames like "test.elisa"; each Analyze call
// runs before the next New() for the same name in practice).
func ParitySourceFor(filename string) (string, bool) {
	if !parityRecordEnabled {
		return "", false
	}
	paritySourceMu.Lock()
	defer paritySourceMu.Unlock()
	src, ok := paritySources[filename]
	return src, ok
}
