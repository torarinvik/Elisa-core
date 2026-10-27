//go:build cgo

package backend

import (
	"errors"
	"sync"
	"testing"
)

func TestPartitionWorkerDeclineStopsQueuedWork(t *testing.T) {
	var called []int
	err := runPartitionIRWorkers(4, 1, []int{0, 1, 2, 3}, func(part int) error { called = append(called, part); return errPartitionDeclined })
	if !errors.Is(err, errPartitionDeclined) || len(called) != 1 || called[0] != 0 {
		t.Fatalf("err=%v calls=%v", err, called)
	}
}

func TestPartitionWorkerWaitsForInflightAndPreservesError(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	failure := errors.New("real compilation error")
	var once sync.Once
	go func() {
		done <- runPartitionIRWorkers(2, 2, []int{0, 1}, func(part int) error {
			if part == 0 {
				<-started
				return errPartitionDeclined
			}
			once.Do(func() { close(started) })
			<-release
			return failure
		})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("returned before in-flight worker finished: %v", err)
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("lost real compilation error: %v", err)
	}
}
