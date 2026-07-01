package parser

import (
	"strings"
	"testing"
)

func TestParseEnumMapRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "enum map routine_kind: TokenKind -> RoutineKind:\n    NEW => NEW\n    STANDARD_ASSIGN => ASSIGN\n    _ => RESET\n")
	if len(errs) == 0 {
		t.Fatalf("expected `enum map` to be rejected, but parse succeeded")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "enum map") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a removal diagnostic mentioning `enum map`, got: %v", errs)
	}
}
