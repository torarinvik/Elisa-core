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
	paritySources       map[string]paritySourceEntry
	parityRecordEnabled = os.Getenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT") != "" ||
		os.Getenv("ELISA_BACKEND_PARITY_OUT") != ""
)

type paritySourceEntry struct {
	source    string
	consumers uint8
}

func paritySourceConsumerCount() uint8 {
	var consumers uint8
	if os.Getenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT") != "" {
		consumers++
	}
	if os.Getenv("ELISA_BACKEND_PARITY_OUT") != "" {
		consumers++
	}
	return consumers
}

func recordParitySource(filename string, src []byte) {
	if !parityRecordEnabled {
		return
	}
	consumers := paritySourceConsumerCount()
	if consumers == 0 {
		return
	}
	paritySourceMu.Lock()
	defer paritySourceMu.Unlock()
	if paritySources == nil {
		paritySources = make(map[string]paritySourceEntry)
	}
	// A filename is the identity used by the recorders. Re-lexing it replaces the prior
	// entry, so repeated in-process checks do not retain every historical source snapshot.
	paritySources[filename] = paritySourceEntry{source: string(src), consumers: consumers}
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
	entry, ok := paritySources[filename]
	return entry.source, ok
}

// ConsumeParitySource returns the source and releases one recorder's ownership of it.
// The semantic and backend parity recorders can both consume the same source when both
// output modes are enabled; after the final consumer, the global map no longer retains
// the source bytes (or their backing allocation).
func ConsumeParitySource(filename string) (string, bool) {
	if !parityRecordEnabled {
		return "", false
	}
	paritySourceMu.Lock()
	defer paritySourceMu.Unlock()
	entry, ok := paritySources[filename]
	if !ok {
		return "", false
	}
	src := entry.source
	if entry.consumers <= 1 {
		delete(paritySources, filename)
	} else {
		entry.consumers--
		paritySources[filename] = entry
	}
	return src, true
}

// DropParitySource releases a source whose parse never reached a parity recorder.
// This is used on lexer/parser failures and is intentionally a no-op when parity is off.
func DropParitySource(filename string) {
	if !parityRecordEnabled {
		return
	}
	paritySourceMu.Lock()
	defer paritySourceMu.Unlock()
	delete(paritySources, filename)
}
