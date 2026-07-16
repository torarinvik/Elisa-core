package parser

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

var parserParityRecordMu sync.Mutex

// recordParserParityCase emits the actual source and oracle outcome exercised by
// the stage0 parser suite. It is inert unless ELISA_PARSER_PARITY_OUT is set, so
// ordinary unit-test behavior and timing are unchanged.
func recordParserParityCase(t *testing.T, src string, errors, notices int) {
	t.Helper()
	path := os.Getenv("ELISA_PARSER_PARITY_OUT")
	if path == "" {
		return
	}
	name := strings.ReplaceAll(t.Name(), "\t", " ")
	encoded := base64.StdEncoding.EncodeToString([]byte(src))
	line := fmt.Sprintf("%s\t%d\t%d\t%s\n", name, errors, notices, encoded)

	parserParityRecordMu.Lock()
	defer parserParityRecordMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open parser parity record: %v", err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		t.Fatalf("write parser parity record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close parser parity record: %v", err)
	}
}
