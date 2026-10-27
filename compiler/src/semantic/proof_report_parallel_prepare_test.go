package semantic

import (
	"fmt"
	"testing"
)

// The source graph may be mutated after preparation returns. The witness walk
// must therefore complete even when binding preparation unwinds with a panic.
func TestProofReportInputPreparationJoinsOnPanic(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	panicking := make(chan struct{})
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		prepareProofReportInputs(func() proofReportCoordinateWitnesses {
			close(started)
			<-release
			return proofReportCoordinateWitnesses{}
		}, func() proofReportBindingRenderer {
			<-started
			close(panicking)
			panic("binding failure")
		})
	}()
	<-panicking
	select {
	case result := <-done:
		close(release)
		t.Fatalf("preparation returned before witness finished: %v", result)
	default:
	}
	close(release)
	if result := <-done; result != "binding failure" {
		t.Fatalf("panic not preserved: %v", result)
	}
}

func TestProofReportInputPreparationForwardsWitnessPanic(t *testing.T) {
	for _, value := range []any{"witness failure", nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			returned := false
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				prepareProofReportInputs(func() proofReportCoordinateWitnesses {
					panic(value)
				}, func() proofReportBindingRenderer { return nil })
				returned = true
			}()
			if returned || value != nil && recovered != value {
				t.Fatalf("witness panic lost: returned=%v recovered=%v", returned, recovered)
			}
		})
	}
}

func TestProofReportInputPreparationPreservesBindingPanic(t *testing.T) {
	started := make(chan struct{})
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		prepareProofReportInputs(func() proofReportCoordinateWitnesses {
			close(started)
			return proofReportCoordinateWitnesses{}
		}, func() proofReportBindingRenderer {
			<-started
			panic("binding failure")
		})
	}()
	if recovered != "binding failure" {
		t.Fatalf("binding panic replaced: %v", recovered)
	}
}

func TestProofReportInputPreparationWitnessPanicTakesPrecedence(t *testing.T) {
	started := make(chan struct{})
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		prepareProofReportInputs(func() proofReportCoordinateWitnesses {
			close(started)
			panic("witness failure")
		}, func() proofReportBindingRenderer {
			<-started
			panic("binding failure")
		})
	}()
	if recovered != "witness failure" {
		t.Fatalf("original witness panic was replaced: %v", recovered)
	}
}
