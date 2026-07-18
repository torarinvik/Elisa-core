package semantic

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Internal-parity recorder (stage1 differential oracle over the FULL in-package
// semantic test suite, not just the curated end-to-end corpus). When
// ELISA_SEMANTIC_INTERNAL_PARITY_OUT names a file, every AnalyzeWithOptions call
// whose source text is known to the lexer registry appends one TSV row:
//
//	b64(filename) \t errors \t warnings \t b64(options-fingerprint) \t b64(source) \t b64(messages)
//
// messages = newline-joined "line<TAB>severity<TAB>message" rows. Inert unless
// the environment variable is set. Rows are appended (not deduplicated) — the
// consuming diff script dedupes by (options, source).
var internalParityMu sync.Mutex

func recordInternalParityCase(file *ast.File, options AnalyzeOptions, result *Result) {
	path := os.Getenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT")
	if path == "" || file == nil || result == nil {
		return
	}
	src, ok := lexer.ParitySourceFor(file.Filename)
	if !ok {
		return
	}
	errors, warnings := 0, 0
	var messages []string
	for _, d := range result.Diagnostics {
		switch d.Severity {
		case DiagnosticSeverityWarning:
			warnings++
		default:
			errors++
		}
		msg := strings.ReplaceAll(strings.ReplaceAll(d.Message, "\t", " "), "\n", " ")
		messages = append(messages, fmt.Sprintf("%d\t%s\t%s", d.Pos.Line, d.Severity, msg))
	}
	// Fingerprint the options that change diagnostic behavior; overlay layouts
	// are pointers (unstable across runs) so only their presence is recorded.
	fp := options
	hasOverlay := len(fp.OverlayLayouts) > 0
	fp.OverlayLayouts = nil
	fingerprint := fmt.Sprintf("%+v;overlay=%t", fp, hasOverlay)

	line := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\t%s\n",
		base64.StdEncoding.EncodeToString([]byte(file.Filename)),
		errors, warnings,
		base64.StdEncoding.EncodeToString([]byte(fingerprint)),
		base64.StdEncoding.EncodeToString([]byte(src)),
		base64.StdEncoding.EncodeToString([]byte(strings.Join(messages, "\n"))))

	internalParityMu.Lock()
	defer internalParityMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
