package interpreter

import (
	"elisacore/src/ast"
	"fmt"
)

func (i *Interpreter) execStmt(frame *frame, stmt ast.Stmt) (controlSignal, error) {
	if err := i.debugRecord(frame, DebugBeforeStmt, stmt); err != nil {
		return controlSignal{}, err
	}
	signal, err := i.execStmtCore(frame, stmt)
	if err != nil {
		return controlSignal{}, err
	}
	if err := i.debugRecord(frame, DebugAfterStmt, stmt); err != nil {
		return controlSignal{}, err
	}
	return signal, nil
}

func (i *Interpreter) execStmtCore(frame *frame, stmt ast.Stmt) (controlSignal, error) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		// Ghost declarations are verification-only and must be erased at runtime,
		// matching the LLVM backend's erasure semantics. The analyzer guarantees no
		// real code reads a ghost variable, so skipping execution is always safe.
		if n.Ghost {
			return controlSignal{}, nil
		}
		value, err := i.evaluateInitializer(frame, n.Type, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		frame.locals[n.Name] = value
		return controlSignal{}, nil
	case *ast.AssignStmt:
		if n.ArgManifest {
			// docs/120 §8: `x <- x.method(…)` — evaluate the void call (its refs mutate x in
			// place); the target is only a manifest, nothing to assign.
			if _, err := i.evalExpr(frame, n.Value); err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			return controlSignal{}, nil
		}
		if n.Optional {
			if err := i.execOptionalAssignStmt(frame, n); err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			return controlSignal{}, nil
		}
		value, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		slot, err := i.resolveSlot(frame, n.Target)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if err := slot.set(value); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			_, err := i.evalExpr(frame, n.CollectionAppend)
			return controlSignal{}, err
		}
		slot, err := i.resolveSlot(frame, n.Target)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		current := slot.get()
		rhs, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		updated, err := evalBinaryOp(augAssignBaseOp(n.Op), current, rhs)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if err := slot.set(updated); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			return controlSignal{kind: signalReturn, value: VoidValue()}, nil
		}
		value, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{kind: signalReturn, value: value}, nil
	case *ast.IfStmt:
		if truth, err := i.evalCondition(frame, n.Cond); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		} else if truth {
			return i.execBlock(frame, n.Then)
		}
		for _, clause := range n.Elifs {
			truth, err := i.evalCondition(frame, clause.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			if truth {
				return i.execBlock(frame, clause.Body)
			}
		}
		return i.execBlock(frame, n.Else)
	case *ast.WhileStmt:
		for iteration := 0; iteration < maxLoopIterations; iteration++ {
			cond, err := i.evalExpr(frame, n.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			truth, err := requireBool(cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			if !truth {
				return controlSignal{}, nil
			}
			signal, err := i.execBlock(frame, n.Body)
			if err != nil {
				return controlSignal{}, err
			}
			if signal.kind != signalNone {
				return signal, nil
			}
		}
		return controlSignal{}, annotateRuntimeError(n.Pos(), fmt.Errorf("loop iteration limit exceeded (%d)", maxLoopIterations))
	case *ast.PassStmt:
		return controlSignal{}, nil
	case *ast.SignalStmt:
		return controlSignal{}, nil
	case *ast.PanicStmt:
		message, err := i.evalExpr(frame, n.Message)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		text, err := stringifyValue(message)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, fmt.Errorf("panic at %s: %s", n.Pos(), text)
	case *ast.ExprStmt:
		_, err := i.evalExpr(frame, n.Expr)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.AssertHoleStmt:
		return controlSignal{}, nil
	case *ast.AssertByStmt:
		return controlSignal{}, nil
	case *ast.ProofBlockStmt:
		return controlSignal{}, nil
	case *ast.DiscardStmt:
		_, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.CanStmt:
		return i.execBlock(frame, n.Body)
	case *ast.RegionStmt:
		// The interpreter has its own (GC'd) memory model, so a region scope — whether
		// written explicitly or synthesized by inference (`in auto:`, the per-function auto
		// region) — is just a transparent block: run its body, no separate arena.
		return i.execBlock(frame, n.Body)
	case *ast.InStoreStmt:
		// Same memory-model transparency as RegionStmt: `in <arena>:` / `in perm:` only
		// route allocations, which the interpreter's GC'd values don't model.
		return i.execBlock(frame, n.Body)
	case *ast.StaticIfStmt:
		cond, err := i.evalExpr(frame, n.Cond)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		truth, err := requireBool(cond)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if truth {
			return i.execBlock(frame, n.Then)
		}
		for _, clause := range n.Elifs {
			value, err := i.evalExpr(frame, clause.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			truth, err := requireBool(value)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			if truth {
				return i.execBlock(frame, clause.Body)
			}
		}
		return i.execBlock(frame, n.Else)
	case *ast.MatchStmt:
		return i.execMatchStmt(frame, n)
	case *ast.MachineCoverageStmt:
		// Machine coverage is a compile-time semantic obligation attached by the
		// parser's desugaring. The runtime machine is already represented by the
		// lowered input read and dispatch statements around this marker, so the
		// interpreter must treat it exactly like the LLVM backend: as a no-op.
		return controlSignal{}, nil
	default:
		return controlSignal{}, annotateRuntimeError(stmt.Pos(), fmt.Errorf("unsupported interpreter statement %T", stmt))
	}
}
func (i *Interpreter) evaluateInitializer(frame *frame, typ ast.TypeExpr, expr ast.Expr) (Value, error) {
	if expr == nil {
		if typ == nil {
			return VoidValue(), nil
		}
		return i.zeroValueForType(typ)
	}
	if _, ok := expr.(*ast.ZeroedLit); ok {
		if typ == nil {
			return VoidValue(), fmt.Errorf("zeroed literal requires an explicit type")
		}
		return i.zeroValueForType(typ)
	}
	return i.evalExpr(frame, expr)
}
