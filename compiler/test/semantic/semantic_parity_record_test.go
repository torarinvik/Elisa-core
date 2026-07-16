package semantic_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

var semanticParityRecordMu sync.Mutex

// recordSemanticParityCase emits end-to-end semantic sources and outcomes for
// stage1 differential replay. It is inert during ordinary test runs.
func recordSemanticParityCase(t *testing.T, filename, src string, errors, warnings int) {
	t.Helper()
	path := os.Getenv("ELISA_SEMANTIC_PARITY_OUT")
	if path == "" {
		return
	}
	name := strings.ReplaceAll(t.Name(), "\t", " ")
	encodedFilename := base64.StdEncoding.EncodeToString([]byte(filename))
	encodedSource := base64.StdEncoding.EncodeToString([]byte(src))
	line := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\n", name, errors, warnings, encodedFilename, encodedSource)

	semanticParityRecordMu.Lock()
	defer semanticParityRecordMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open semantic parity record: %v", err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		t.Fatalf("write semantic parity record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close semantic parity record: %v", err)
	}
}
