//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitLocalVarDecl(n *ast.VarDeclStmt) error {
	if n.Ghost || (s.g != nil && s.g.result != nil && s.g.semanticReadView().ValueGhostDecls(n)) {
		// A `ghost` declaration is verification-only: the analyzer proved no real value depends on
		// it, so emit NOTHING (the variable, its initializer, and any side effects are erased).
		return nil
	}
	var declType semantic.Type
	var err error
	if n.Type != nil {
		declType, err = s.resolveTypeExpr(n.Type)
		if err != nil {
			return err
		}
	} else if n.Value != nil {
		declType = s.exprType(n.Value)
		if declType == nil {
			return fmt.Errorf("cannot infer type for variable %s", n.Name)
		}
	} else {
		return fmt.Errorf("variable %s requires a type or initializer", n.Name)
	}
	alloca, err := s.createEntryAlloca(n.Name, declType)
	if err != nil {
		return err
	}
	// The binding comes into scope AFTER its initializer is emitted, not before.
	//
	// A declaration that SHADOWS an outer local reads the outer one in its own
	// initializer — `i = i + 1` inside a loop body means "declare a new i, one more
	// than the enclosing i". Defining the binding first made `i` inside the
	// initializer resolve to the slot being declared, which nothing has stored to
	// yet: the emitted IR loads the fresh alloca, so the new local is
	// uninitialized-memory + 1 and the OUTER i never changes.
	//
	// Measured on `def main() -> i64: i: mutable i64 = 0; while i < 3: i = i + 1;
	// return i`: the INTERPRETER answers 3 and the native binary loops forever. Two
	// answers from one compiler, and the interpreter's is the correct one — a
	// declaration's initializer belongs to the enclosing scope.
	var initValue llvmValueRef
	if n.Value != nil {
		savedInitializerCarriesRegionStorage := s.activeInitializerCarriesRegionStorage
		savedInitializerName := s.activeInitializerName
		s.activeInitializerCarriesRegionStorage = backendTypeCarriesRegionStorage(declType)
		s.activeInitializerName = n.Name
		defer func() {
			s.activeInitializerCarriesRegionStorage = savedInitializerCarriesRegionStorage
			s.activeInitializerName = savedInitializerName
		}()
		// A returned-container call in an adopted region-polymorphic function must
		// use the fresh local's owner, not the caller's ambient owner. Otherwise a
		// later growth of a caller-owned darray sees the temporary result after its
		// own backing allocation and reserve_commit correctly rejects the non-tail
		// realloc. Escaping locals retain the adopted caller owner.
		savedTreeOwner := s.treeAllocOwner
		// The owner also routes growth arenas of OTHER locals the initializer mutates
		// (`e: E = name(sub)` grows sub.store through an lmut param), so an initializer
		// that names an escaping local keeps the caller owner.
		if scratch := s.adoptedScratchForLocal(n.Name); scratch != nil && !exprNamesAnyOf(n.Value, s.adoptedEscapeNames) {
			s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: scratch}
			defer func() { s.treeAllocOwner = savedTreeOwner }()
		}
		// If this local is a stack-tagged darray, tell a seeded container initializer to
		// allocate its initial backing into that SAME parallel arena (consumed once by the
		// literal/comprehension emit), so later growth through a grower never straddles the
		// region base arena and the parallel arena (task_00a7fdf3).
		savedSink := s.currentDArraySinkTag
		if s.darrayStackTag != nil {
			if tag, ok := s.darrayStackTag[n.Name]; ok {
				s.currentDArraySinkTag = tag
			}
		}
		value, _, err := s.emitExpr(n.Value, declType)
		s.currentDArraySinkTag = savedSink
		if err != nil {
			return err
		}
		initValue = value
		// storeValue lowers large `zeroed` aggregates to llvm.memset and large
		// aggregate copies to memcpy, avoiding `store <bigtype> zeroinitializer`
		// (catastrophic for llc -O0 on multi-KB structs).
		if err := s.storeValue(alloca, value, declType, n.Name); err != nil {
			callContext := ""
			if call, ok := n.Value.(*ast.CallExpr); ok {
				targetName := fmt.Sprintf("%T", call.Func)
				switch target := call.Func.(type) {
				case *ast.Ident:
					targetName = target.Name
				case *ast.FieldExpr:
					targetName = target.Field
				}
				callContext = fmt.Sprintf(", call target %q has semantic type %v", targetName, s.exprType(call.Func))
			}
			return fmt.Errorf("while emitting %s local %q at source line %d (expr %T has semantic type %v%s): %w", s.decl.Name, n.Name, n.Pos().Line, n.Value, s.exprType(n.Value), callContext, err)
		}
		s.bindPackedStoreValue(declType, value)
		if err := s.bindPackedStoreOriginsForExprPath(n.Name, n.Value, declType); err != nil {
			return err
		}
	} else {
		// Omitted initializers have the same zero value as the interpreter and Stage1.
		// Store at the declaration, so loop-local declarations reset on every iteration.
		initValue, err = s.zeroValue(declType)
		if err != nil {
			return err
		}
		if err := s.storeValue(alloca, initValue, declType, n.Name); err != nil {
			return fmt.Errorf("while zero-initializing local %q: %w", n.Name, err)
		}
	}
	s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType, mutable: n.Mutable})
	// docs/126 D1: a drop-typed local arms its destructor here, after the binding is
	// in scope (the synthesized `__drop__(move n.Name)` resolves through it) and
	// after the initializer ran (a shadowing decl must not arm the outer value).
	if _, err := s.registerDropCleanup(n.Name, alloca, declType); err != nil {
		return err
	}
	if s.g.di != nil {
		s.g.di.declareVariable(s, n.Name, alloca, declType, n.Pos().Line, 0)
	}
	s.bindImplicitTreeOwnerParam(n.Name, declType, alloca, initValue)
	if s.g.trace != nil {
		s.g.trace.recordValue(s, n.Pos().Line, n.Name, initValue, declType)
	}
	if err := s.emitRefinementChecks(n); err != nil {
		return err
	}
	return nil
}
