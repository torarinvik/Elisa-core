package semantic

import (
	"fmt"
	"math"
	"testing"

	"elisacore/src/lexer"
)

func TestRetainedLineKeyCompatibility(t *testing.T) {
	for _, p := range []lexer.Pos{
		{},
		{File: "file.elisa", Line: 1, Col: 42},
		{File: "some:path/æ.elisa", Line: -1, Col: 0},
		{File: "boundary", Line: math.MinInt, Col: math.MaxInt},
	} {
		want := fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
		if got := retainedLineKey(p); got != want {
			t.Fatalf("retainedLineKey(%+v) = %q, want %q", p, got, want)
		}
	}
}
