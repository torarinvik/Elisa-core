//go:build cgo

package backend

import "sync"

// llvmDisposalScope bounds asynchronous module/context teardown for one object
// build. The caller waits only after all generators in that build have stopped
// submitting disposals, so WaitGroup.Add never races a zero-count Wait.
type llvmDisposalScope struct {
	slots   chan struct{}
	pending sync.WaitGroup
}

func newLLVMDisposalScope(maxConcurrent int) *llvmDisposalScope {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &llvmDisposalScope{slots: make(chan struct{}, maxConcurrent)}
}

// schedule keeps the number of live LLVM module/context frees bounded. When
// the bound is full, the submitting codegen worker waits for an existing free
// to finish instead of adding another goroutine or C allocation to a backlog.
func (s *llvmDisposalScope) schedule(dispose func()) {
	if dispose == nil {
		return
	}
	if s == nil {
		dispose()
		return
	}
	s.slots <- struct{}{}
	s.pending.Add(1)
	go func() {
		defer func() {
			<-s.slots
			s.pending.Done()
		}()
		dispose()
	}()
}

// wait drains this build's C teardown before its caller can return or begin a
// later retained request.
func (s *llvmDisposalScope) wait() {
	if s != nil {
		s.pending.Wait()
	}
}
