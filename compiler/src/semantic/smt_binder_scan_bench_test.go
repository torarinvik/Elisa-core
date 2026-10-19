package semantic

import (
	"fmt"
	"strings"
	"testing"
)

var smtBinderScanSink bool

func BenchmarkSMTBinderScan(b *testing.B) {
	many := strings.Builder{}
	many.WriteString("(forall (")
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&many, "(q_%d Int) ", i)
	}
	many.WriteString(") (and ")
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&many, "(= q_%d 1) ", i%32)
	}
	many.WriteString("))")
	for _, c := range []struct{ name, s string }{{"plain-short", "(= (+ offset 1) length)"}, {"plain-long", strings.Repeat("(= (+ offset 1) length) ", 128)}, {"free-early", "q_free " + strings.Repeat("(= (+ offset 1) length) ", 128)}, {"bound-one", "(forall ((q_a Int)) (= q_a 1))"}, {"bound-many", many.String()}, {"free-last", many.String() + " q_other"}, {"free-late-plain", strings.Repeat("(= (+ offset 1) length) ", 128) + " q_late"}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(c.s)))
			for i := 0; i < b.N; i++ {
				smtBinderScanSink = smtTermMentionsQuantifierBinder(c.s)
			}
		})
	}
}
