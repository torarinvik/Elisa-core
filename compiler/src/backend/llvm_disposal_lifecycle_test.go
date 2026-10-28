//go:build cgo

package backend

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestLLVMDisposalScopeBoundsAndJoinsCleanup(t *testing.T) {
	const limit = 2
	const jobs = 4
	scope := newLLVMDisposalScope(limit)
	started := make(chan struct{}, jobs)
	submitted := make(chan struct{}, jobs)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32

	for range jobs {
		go func() {
			scope.schedule(func() {
				current := active.Add(1)
				for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
			})
			submitted <- struct{}{}
		}()
	}

	for range limit {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("disposal workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("disposal scope exceeded its concurrency limit")
	case <-time.After(25 * time.Millisecond):
	}
	if got := peak.Load(); got > limit {
		t.Fatalf("peak active disposals=%d, limit=%d", got, limit)
	}

	close(release)
	for range jobs {
		select {
		case <-submitted:
		case <-time.After(time.Second):
			t.Fatal("disposal submission did not complete")
		}
	}

	joined := make(chan struct{})
	go func() {
		scope.wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("disposal scope did not join completed cleanup")
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("cleanup remained active after wait: %d", got)
	}
	if got := peak.Load(); got > limit {
		t.Fatalf("peak active disposals=%d, limit=%d", got, limit)
	}

	joinScope := newLLVMDisposalScope(1)
	joinStarted := make(chan struct{})
	joinRelease := make(chan struct{})
	joinScope.schedule(func() {
		close(joinStarted)
		<-joinRelease
	})
	<-joinStarted
	joined = make(chan struct{})
	go func() {
		joinScope.wait()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("wait returned while cleanup was still running")
	case <-time.After(25 * time.Millisecond):
	}
	close(joinRelease)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("wait did not return after cleanup completed")
	}
}
