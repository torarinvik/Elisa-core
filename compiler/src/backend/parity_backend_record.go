package backend

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

// Backend-parity recorder (stage1 differential oracle over stage0's LLVM-lowering
// test suites, src/backend + test/backend). When ELISA_BACKEND_PARITY_OUT names a
// file, every textual-IR lowering appends one TSV row:
//
//	b64(filename) \t ok \t optLevel \t b64(source) \t b64(error)
//
// `ok` is 1 when stage0's backend lowered the program to IR and 0 when it returned
// an error. The consuming replay script feeds each source to stage1 and requires
// stage1 to lower whatever stage0 lowered — a source stage0 turns into IR but
// stage1 declines is a backend FEATURE GAP, which is exactly what this corpus is
// for. Inert (one env lookup, already cached by the lexer gate) unless the
// variable is set. Rows are appended, not deduplicated; the consumer dedupes by
// (optLevel, source).
var backendParityMu sync.Mutex

func recordBackendParityCase(result *semantic.Result, optLevel OptimizationLevel, genErr error) {
	path := os.Getenv("ELISA_BACKEND_PARITY_OUT")
	if path == "" || result == nil || result.File == nil {
		return
	}
	src, ok := lexer.ParitySourceFor(result.File.Filename)
	if !ok {
		return
	}
	lowered := 1
	errText := ""
	if genErr != nil {
		lowered = 0
		errText = strings.ReplaceAll(strings.ReplaceAll(genErr.Error(), "\t", " "), "\n", " ")
	}
	line := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\n",
		base64.StdEncoding.EncodeToString([]byte(result.File.Filename)),
		lowered, int(optLevel),
		base64.StdEncoding.EncodeToString([]byte(src)),
		base64.StdEncoding.EncodeToString([]byte(errText)))

	backendParityMu.Lock()
	defer backendParityMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
