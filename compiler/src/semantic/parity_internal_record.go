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
	// regionStackCap is an analyzer package global (not an AnalyzeOptions field), but a family of
	// region-lifetime tests OVERRIDE it (to 4) to exercise the merge-stack rejection path. Its value
	// materially changes region diagnostics, so record it in the fingerprint — otherwise two cases
	// with the same source but different caps collapse to contradictory rows the stage1 replay
	// cannot disambiguate (it is not spelled in the source). stage1 maps a non-default cap to a
	// `# regioncap N` replay header.
	fingerprint := fmt.Sprintf("%+v;overlay=%t;regionStackCap=%d", fp, hasOverlay, regionStackCap)

	// Overlay layouts are injected via AnalyzeOptions (no in-source spelling in these
	// tests), so serialize them as a 7th column: one line per layout,
	// "name|size|stride|field:offset:width:reqsize,...". stage1's replay turns each
	// into a `# overlay ...` header the ported overlay checks consume.
	var overlaySpecs []string
	for _, layout := range options.OverlayLayouts {
		if layout == nil {
			continue
		}
		fields := make([]string, 0, len(layout.Fields))
		for _, f := range layout.Fields {
			fields = append(fields, fmt.Sprintf("%s:%d:%d:%d", f.Name, f.Offset, f.Width, f.RequiresSizeAtLeast))
		}
		overlaySpecs = append(overlaySpecs, fmt.Sprintf("%s|%d|%d|%s", layout.Name, layout.Size, layout.Stride, strings.Join(fields, ",")))
	}
	line := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\t%s\t%s\n",
		base64.StdEncoding.EncodeToString([]byte(file.Filename)),
		errors, warnings,
		base64.StdEncoding.EncodeToString([]byte(fingerprint)),
		base64.StdEncoding.EncodeToString([]byte(src)),
		base64.StdEncoding.EncodeToString([]byte(strings.Join(messages, "\n"))),
		base64.StdEncoding.EncodeToString([]byte(strings.Join(overlaySpecs, "\n"))))

	internalParityMu.Lock()
	defer internalParityMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
