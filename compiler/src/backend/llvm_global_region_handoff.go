//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// globalHandoffShape identifies values the clone emitter can fully own.
// Preserve the existing lowering for scalars and unsupported borrowed fields.
func (g *llvmGenerator) globalHandoffShape(t semantic.Type, active map[semantic.Type]bool) (supported, ownsBacking bool) {
	t = semantic.StripAggregateStateType(t)
	switch t.(type) {
	case *semantic.BuiltinType, *semantic.IDType, *semantic.ConstEnumType:
		return true, false
	case *semantic.OptionalType, *semantic.ArrayType, *semantic.DArrayType,
		*semantic.StructType, *semantic.TupleType, *semantic.GenericInstanceType:
	default:
		return false, false
	}
	if active[t] {
		// The clone emitter expands element types recursively. A recursive
		// layout needs a different clone strategy.
		return false, false
	}
	active[t] = true
	defer delete(active, t)
	switch t := t.(type) {
	case *semantic.OptionalType:
		return g.globalHandoffShape(t.Value, active)
	case *semantic.ArrayType:
		if !t.HasConstSize {
			return false, false
		}
		return g.globalHandoffShape(t.Elem, active)
	case *semantic.DArrayType:
		ok, _ := g.globalHandoffShape(t.Elem, active)
		return ok && t.Region == "", true
	case *semantic.StructType, *semantic.TupleType, *semantic.GenericInstanceType:
		fields, err := g.structLiteralFields(t)
		if err != nil {
			return false, false
		}
		for _, field := range fields {
			ok, backing := g.globalHandoffShape(field.Type, active)
			if !ok {
				return false, false
			}
			ownsBacking = ownsBacking || backing
		}
		return true, ownsBacking
	default:
		return false, false
	}
}

// Replacing a global value preserves one previous backing generation for
// aliases into the old value. Clone before publishing the new descriptor.
func (s *functionState) emitGlobalRegionHandoff(target ast.Expr, ptr, value llvmIRValueRef, typ semantic.Type) (llvmIRValueRef, error) {
	ident, ok := stripTreeAllocOwnerExpr(target).(*ast.Ident)
	if !ok {
		return value, nil
	}
	if _, local := s.lookupBinding(ident.Name); local {
		return value, nil
	}
	symbol, _, ok := s.lookupVisibleGlobalSymbol(ident.Name)
	if !ok || symbol.Kind != semantic.SymbolGlobal {
		return value, nil
	}
	if supported, backing := s.g.globalHandoffShape(typ, map[semantic.Type]bool{}); !supported || !backing {
		return value, nil
	}
	arenaType := s.g.namedType("Arena")
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return nil, err
	}
	name := llvmGoString(llvmGetValueName(ptr))
	slot := func(suffix string, slotType llvmIRTypeRef) llvmIRValueRef {
		slotName := cStringFree(name + suffix)
		if existing := llvmGetNamedGlobal(s.g.module, slotName); existing != nil {
			return existing
		}
		global := llvmAddGlobal(s.g.module, slotType, slotName)
		llvmSetInitializer(global, llvmConstNull(slotType))
		linkage := llvmIRLinkage(llvmWeakAnyLinkage)
		if llvmGetLinkage(ptr) == llvmInternalLinkage {
			linkage = llvmInternalLinkage
		}
		llvmSetLinkage(global, linkage)
		return global
	}
	first := slot(".handoff.0", arenaLLVMType)
	second := slot(".handoff.1", arenaLLVMType)
	generationType := llvmInt1TypeInContext(s.g.context)
	generation := slot(".handoff.generation", generationType)
	current := llvmBuildLoad2(s.builder, generationType, generation, cStringFree("handoff.current"))
	spare := llvmBuildSelect(s.builder, current, first, second, cStringFree("handoff.spare"))
	scratch, err := s.createEntryAllocaZeroed("handoff.scratch", arenaType)
	if err != nil {
		return nil, err
	}
	savedOwner := s.treeAllocOwner
	s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: scratch}
	cloned, err := s.emitCloneValue(value, typ, typ, "handoff")
	s.treeAllocOwner = savedOwner
	if err != nil {
		return nil, err
	}
	// The input can alias the spare generation. Retire it only after cloning.
	if err := s.emitArenaFree(spare, arenaType); err != nil {
		return nil, err
	}
	if err := s.emitArenaAdopt(spare, scratch, arenaType); err != nil {
		return nil, err
	}
	next := llvmBuildXor(s.builder, current, llvmConstInt(generationType, 1, 0), cStringFree("handoff.next"))
	llvmBuildStore(s.builder, next, generation)
	return cloned, nil
}
