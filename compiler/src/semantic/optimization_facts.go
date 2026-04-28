package semantic

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
	Exclusive             bool
	ReadOnly              bool
	Contiguous            bool
	UnitStride            bool
	FrozenPackedStoreOnly bool
	PackedStoreProvenance PackedStoreProvenance
	Extent                *OptimizationExtent
	base                  string
}

type optimizationAffineExpr struct {
	Const int64
	Terms map[string]int64
}

func (p PackedStoreProvenance) HasAnyFacts() bool {
	return p.HasPackedStoreDeps || p.HasFrozenPackedStoreDeps || p.HasNonFrozenPackedStoreDeps || p.HasNonStoreProvenance
}

func (p PackedStoreProvenance) HasAnyPackedStoreProvenance() bool {
	return p.HasPackedStoreDeps
}

func (p PackedStoreProvenance) DependsOnFrozenPackedStores() bool {
	return p.HasFrozenPackedStoreDeps
}

func (p PackedStoreProvenance) DependsOnNonFrozenPackedStores() bool {
	return p.HasNonFrozenPackedStoreDeps
}

func (p PackedStoreProvenance) HasOnlyFrozenPackedStoreDeps() bool {
	return p.HasPackedStoreDeps && p.HasFrozenPackedStoreDeps && !p.HasNonFrozenPackedStoreDeps
}

func (p PackedStoreProvenance) DependsOnlyOnFrozenPackedStores() bool {
	return p.HasPackedStoreDeps && p.HasFrozenPackedStoreDeps && !p.HasNonFrozenPackedStoreDeps && !p.HasNonStoreProvenance
}

func (p PackedStoreProvenance) HasMixedProvenance() bool {
	return p.HasNonStoreProvenance || (p.HasFrozenPackedStoreDeps && p.HasNonFrozenPackedStoreDeps)
}
