//go:build cgo

package backend

import (
	"elisacore/src/ast"
)

func (s *functionState) emitDeferredBody(binding *deferredBodyBinding) error {
	if binding == nil || binding.stmt == nil {
		return nil
	}
	s.pushScope()
	defer s.popScope()
	s.injectCapturedScope(binding.captureScope)
	return s.emitBlockInCurrentScope(binding.stmt.Body)
}
func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	// Invariants declared inside this block stop being re-checked once it exits (their variables may
	// leave scope). Truncate back to the count we entered with (docs/90 brick 90-14).
	savedInvariants := len(s.activeInvariants)
	defer func() { s.activeInvariants = s.activeInvariants[:savedInvariants] }()
	if scoped {
		savedPackedStores := s.packedStores
		s.packedStores = s.clonePackedStores()
		s.pushScope()
		scope := s.scope
		defer func() {
			s.popScope()
			s.packedStores = savedPackedStores
		}()
		for _, stmt := range stmts {
			if s.currentBlockTerminated() {
				break
			}
			if err := s.emitStmt(stmt); err != nil {
				return err
			}
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil
		}
		return s.emitScopeCleanups(scope)
	}
	for _, stmt := range stmts {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitStmt(stmt ast.Stmt) error {
	if err := s.emitStmtInner(stmt); err != nil {
		return err
	}
	if err := s.recheckInvariantsAfter(stmt); err != nil {
		return err
	}
	return s.maybeEarlyFreeAfter(stmt)
}

// maybeEarlyFreeAfter frees the own-stack arenas whose objects' lives end with this top-level
// statement (Phase B2). Idempotent with the region-exit cleanup (arena_free nulls begin/end);
// fired once per statement and skipped if the block already terminated.
func (s *functionState) maybeEarlyFreeAfter(stmt ast.Stmt) error {
	if s.earlyFreeByOffset == nil || stmt == nil || s.currentBlockTerminated() {
		return nil
	}
	arenaPtrs, ok := s.earlyFreeByOffset[stmt.Pos().Offset]
	if !ok {
		return nil
	}
	delete(s.earlyFreeByOffset, stmt.Pos().Offset)
	arenaType := s.g.namedType("Arena")
	for _, arenaPtr := range arenaPtrs {
		if err := s.emitArenaFree(arenaPtr, arenaType); err != nil {
			return err
		}
	}
	return nil
}
