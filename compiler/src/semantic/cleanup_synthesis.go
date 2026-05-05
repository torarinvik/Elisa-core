package semantic

import "elisacore/src/ast"

type TypeBoundCleanupKind string

const (
	TypeBoundCleanupNone               TypeBoundCleanupKind = ""
	TypeBoundCleanupMutexUnlock        TypeBoundCleanupKind = "mutex_unlock"
	TypeBoundCleanupThreadPoolShutdown TypeBoundCleanupKind = "thread_pool_shutdown"
)

type TypeBoundCleanupOp struct {
	Kind     TypeBoundCleanupKind
	Path     []string
	Sequence []TypeBoundCleanupOp
}

type CleanupBindingPlan struct {
	Name string
	Ops  []TypeBoundCleanupOp
}

type CleanupPlan struct {
	Bindings []CleanupBindingPlan
}

func AtomicRefOp(path []string, kind TypeBoundCleanupKind) TypeBoundCleanupOp {
	return TypeBoundCleanupOp{Kind: kind, Path: append([]string(nil), path...)}
}

func FillSeqOp(path []string, elemOps []TypeBoundCleanupOp) TypeBoundCleanupOp {
	return TypeBoundCleanupOp{Path: append([]string(nil), path...), Sequence: cloneTypeBoundCleanupOps(elemOps)}
}

func (op TypeBoundCleanupOp) IsAtomic() bool {
	return op.Kind != TypeBoundCleanupNone
}

func (op TypeBoundCleanupOp) IsFillSeq() bool {
	return op.Kind == TypeBoundCleanupNone && len(op.Sequence) != 0
}

func (op TypeBoundCleanupOp) WithPrependedPath(prefix string) TypeBoundCleanupOp {
	if prefix == "" {
		return op
	}
	cloned := TypeBoundCleanupOp{Kind: op.Kind, Path: make([]string, 0, len(op.Path)+1), Sequence: cloneTypeBoundCleanupOps(op.Sequence)}
	cloned.Path = append(cloned.Path, prefix)
	cloned.Path = append(cloned.Path, op.Path...)
	return cloned
}

func CreateTypeBoundOps(t Type) []TypeBoundCleanupOp {
	return createTypeBoundOps(t, map[string]bool{})
}

func SynthesizeParamCleanupPlan(fn *ast.FuncDecl, fnType *FuncType) CleanupPlan {
	if fn == nil || fnType == nil {
		return CleanupPlan{}
	}
	plan := CleanupPlan{}
	for i := range fnType.Params {
		name := functionParamName(fnType, i)
		if name == "" {
			continue
		}
		ops := CreateTypeBoundOps(fnType.Params[i])
		if len(ops) == 0 {
			continue
		}
		plan.Bindings = append(plan.Bindings, CleanupBindingPlan{Name: name, Ops: ops})
	}
	return plan
}

func createTypeBoundOps(t Type, seen map[string]bool) []TypeBoundCleanupOp {
	if t == nil {
		return nil
	}
	t = StripAggregateStateType(t)
	if kind := cleanupKindForType(t); kind != TypeBoundCleanupNone {
		return []TypeBoundCleanupOp{AtomicRefOp(nil, kind)}
	}
	key := t.String()
	if key != "" {
		if seen[key] {
			return nil
		}
		seen[key] = true
		defer delete(seen, key)
	}
	switch tt := t.(type) {
	case *StructType:
		return createStructCleanupOps(tt.Fields, seen)
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok && base != nil {
			return createStructCleanupOps(base.Fields, seen)
		}
	case *ArrayType:
		elemOps := createTypeBoundOps(tt.Elem, seen)
		if len(elemOps) != 0 {
			return []TypeBoundCleanupOp{FillSeqOp(nil, elemOps)}
		}
	case *OptionalType:
		return createTypeBoundOps(tt.Value, seen)
	}
	return nil
}

func cleanupKindForType(t Type) TypeBoundCleanupKind {
	switch tt := StripAggregateStateType(t).(type) {
	case *StructType:
		if !tt.Builtin {
			return TypeBoundCleanupNone
		}
		switch tt.Name {
		case "ThreadPool":
			return TypeBoundCleanupThreadPoolShutdown
		}
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil || !base.Builtin {
			return TypeBoundCleanupNone
		}
		switch base.Name {
		case "ThreadPool":
			return TypeBoundCleanupThreadPoolShutdown
		case "MutexGuard":
			if len(tt.Args) >= 1 && tt.Args[0] != nil && tt.Args[0].String() == "Held" {
				return TypeBoundCleanupMutexUnlock
			}
		}
	}
	return TypeBoundCleanupNone
}

func createStructCleanupOps(fields map[string]Field, seen map[string]bool) []TypeBoundCleanupOp {
	if len(fields) == 0 {
		return nil
	}
	ops := make([]TypeBoundCleanupOp, 0, len(fields))
	for name, field := range fields {
		fieldOps := createTypeBoundOps(field.Type, seen)
		for _, op := range fieldOps {
			ops = append(ops, op.WithPrependedPath(name))
		}
	}
	return ops
}

func cloneTypeBoundCleanupOps(src []TypeBoundCleanupOp) []TypeBoundCleanupOp {
	if len(src) == 0 {
		return nil
	}
	out := make([]TypeBoundCleanupOp, len(src))
	for i, op := range src {
		out[i] = TypeBoundCleanupOp{Kind: op.Kind, Path: append([]string(nil), op.Path...), Sequence: cloneTypeBoundCleanupOps(op.Sequence)}
	}
	return out
}
