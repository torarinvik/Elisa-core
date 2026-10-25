package semantic

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRequestedProofCoordinatesKeepFrozenFullRendererAndTraversal(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	old, err := os.ReadFile(filepath.Join(dir, "proof_report_coordinates.go"))
	if err != nil {
		t.Fatal(err)
	}
	reference := string(old)
	start := strings.Index(reference, "func proofReportSyntaxRenderer(")
	if start < 0 {
		t.Fatal("full oracle missing")
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(reference[start:], "\n")))) != "91c48a7639365425bd60d206d4ef376fdb7fbcd3ffaca686d45d155c914b68c5" {
		t.Fatal("full frozen renderer changed; review the actual source oracle")
	}
	filtered, err := os.ReadFile(filepath.Join(dir, "proof_report_requested_coordinates.go"))
	if err != nil {
		t.Fatal(err)
	}
	traversal := func(source string) string {
		begin := strings.Index(source, "\tseen := map[uintptr]bool{}")
		if begin < 0 {
			t.Fatal("complete pointer traversal missing")
		}
		end := strings.Index(source[begin:], "\tvisit(reflect.ValueOf(file))")
		if end < 0 {
			t.Fatal("complete source root visit missing")
		}
		return source[begin : begin+end+len("\tvisit(reflect.ValueOf(file))")]
	}
	if traversal(reference[start:]) != traversal(string(filtered)) {
		t.Fatal("authored witness traversal differs from full Node.Pos/Pos-field oracle")
	}
}
