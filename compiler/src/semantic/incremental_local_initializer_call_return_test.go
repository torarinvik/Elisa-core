//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalLocalInitializerHitDoesNotReuseCallReturningBody(t *testing.T) {
	const source = `def callee(value: i64) -> i64:
    if value > 0:
        return value
    return 0

def work(value: i64) -> i64:
    prepared: i64 = value + 1
    return callee(value)
`

	cold := Analyze(parseIncrementalSessionFile(t, source))
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	assertIncrementalResultParity(t, cold, first)
	if got := session.Stats().LocalInitializersChecked; got != 1 {
		t.Fatalf("cold run did not analyze the supported local initializer: stats=%+v", session.Stats())
	}

	before := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	after := session.Stats()
	if got := after.LocalInitializersReused - before.LocalInitializersReused; got != 1 {
		t.Fatalf("warm run missed the initializer product: reused=%d stats=%+v", got, after)
	}
	if got := after.PrimaryBodiesReused - before.PrimaryBodiesReused; got != 0 {
		t.Fatalf("call-return body was reused with an initializer product: body reuses=%d stats=%+v", got, after)
	}
	if got := strings.Join(warm.Errors(), "\n"); got != strings.Join(cold.Errors(), "\n") {
		t.Fatalf("cold/warm diagnostics differ: cold=%v warm=%v", cold.Errors(), warm.Errors())
	}
	assertIncrementalResultParity(t, cold, warm)
}
