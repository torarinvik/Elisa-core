package semantic

import (
	"fmt"

	"llcontext/src/ast"
)

type OptimizationExtentKind int

const (
	OptimizationExtentNone OptimizationExtentKind = iota
	OptimizationExtentShape
	OptimizationExtentArraySize
	OptimizationExtentViewBounds
)

type OptimizationExtent struct {
	Kind         OptimizationExtentKind
	Shape        Shape
	Size         string
	HasConstSize bool
	ConstSize    int64
	Begin        string
	End          string
}

type OptimizationFacts struct {
	Exclusive  bool
	ReadOnly   bool
	Contiguous bool
	UnitStride bool
	Extent     *OptimizationExtent
}

func (e *OptimizationExtent) String() string {
	if e == nil {
		return "<unknown>"
	}
	switch e.Kind {
	case OptimizationExtentShape:
		if e.Shape == nil {
			return "shape<?>"
		}
		return fmt.Sprintf("shape<%s>", e.Shape.String())
	case OptimizationExtentArraySize:
		if e.HasConstSize {
			return fmt.Sprintf("size<%d>", e.ConstSize)
		}
		return fmt.Sprintf("size<%s>", e.Size)
	case OptimizationExtentViewBounds:
		return fmt.Sprintf("bounds<%s:%s>", e.Begin, e.End)
	default:
		return "<unknown>"
	}
}

func (f OptimizationFacts) HasExactExtent() bool {
	return f.Extent != nil
}

func (f OptimizationFacts) SameExtent(other OptimizationFacts) bool {
	return SameOptimizationExtent(f.Extent, other.Extent)
}

func SameOptimizationExtent(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case OptimizationExtentShape:
		return SameShape(a.Shape, b.Shape)
	case OptimizationExtentArraySize:
		if a.HasConstSize && b.HasConstSize {
			return a.ConstSize == b.ConstSize
		}
		return a.Size == b.Size
	case OptimizationExtentViewBounds:
		return a.Begin == b.Begin && a.End == b.End
	default:
		return false
	}
}

func optimizationFactsForType(t Type) OptimizationFacts {
	if t == nil {
		return OptimizationFacts{}
	}
	switch tt := t.(type) {
	case *RefType:
		facts := optimizationFactsForType(tt.Elem)
		if builtin, ok := tt.Elem.(*BuiltinType); ok && builtin.Name == "u8" && tt.Storage == RefStorageStatic {
			facts.ReadOnly = true
			facts.Contiguous = true
			facts.UnitStride = true
		}
		return facts
	case *ArrayType:
		return OptimizationFacts{
			Contiguous: true,
			UnitStride: true,
			Extent: &OptimizationExtent{
				Kind:         OptimizationExtentArraySize,
				Size:         tt.Size,
				HasConstSize: tt.HasConstSize,
				ConstSize:    tt.ConstSize,
			},
		}
	case *DArrayType:
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if !isWildcardShape(tt.Shape) {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentShape, Shape: tt.Shape}
		}
		return facts
	case *ViewType:
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
	case *DArrayViewType:
		return OptimizationFacts{Contiguous: true, UnitStride: true}
	case *DStrType:
		facts := OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		if !isWildcardShape(tt.Shape) {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentShape, Shape: tt.Shape}
		}
		return facts
	case *SViewType:
		facts := OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
	case *GenericInstanceType:
		if _, ok := dynArrayRuntimeInstance(tt); ok {
			return OptimizationFacts{Contiguous: true, UnitStride: true}
		}
		return OptimizationFacts{}
	case *StructType:
		if dynArrayView, ok := dynArrayViewRuntimeType(tt); ok && dynArrayView != nil {
			return OptimizationFacts{Contiguous: true, UnitStride: true}
		}
		if stringView, ok := stringViewRuntimeType(tt); ok && stringView != nil {
			return OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		}
		return OptimizationFacts{}
	default:
		return OptimizationFacts{}
	}
}

func (a *Analyzer) inferExprOptimizationFacts(expr ast.Expr, t Type) OptimizationFacts {
	facts := optimizationFactsForType(t)
	if expr == nil {
		return facts
	}
	switch expr.(type) {
	case *ast.AllocExpr:
		if _, ok := t.(*RefType); ok {
			facts.Exclusive = true
		}
	case *ast.StringLit:
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
	}
	return facts
}

func (r *Result) ExprOptimizationFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if r == nil || expr == nil || r.ExprFacts == nil {
		return OptimizationFacts{}, false
	}
	facts, ok := r.ExprFacts[expr]
	return facts, ok
}
