package semantic

import (
	"fmt"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
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

type PackedStoreProvenance struct {
	HasPackedStoreDeps          bool
	HasFrozenPackedStoreDeps    bool
	HasNonFrozenPackedStoreDeps bool
	HasNonStoreProvenance       bool
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

func (f OptimizationFacts) SameExtentSize(other OptimizationFacts) bool {
	return SameOptimizationExtentSize(f.Extent, other.Extent)
}

func (f OptimizationFacts) Disjoint(other OptimizationFacts) bool {
	return OptimizationFactsDisjoint(f, other)
}

func (f OptimizationFacts) HasAnyFacts() bool {
	return f.Exclusive ||
		f.ReadOnly ||
		f.Contiguous ||
		f.UnitStride ||
		f.FrozenPackedStoreOnly ||
		f.Extent != nil ||
		f.base != "" ||
		f.PackedStoreProvenance.HasAnyFacts()
}

func OptimizationFactsDisjoint(a, b OptimizationFacts) bool {
	if a.base == "" || b.base == "" {
		return false
	}
	if a.base != b.base {
		return a.Exclusive && b.Exclusive
	}
	return optimizationExtentsDisjoint(a.Extent, b.Extent)
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

func SameOptimizationExtentSize(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if SameOptimizationExtent(a, b) {
		return true
	}
	aSize, aSizeOK := optimizationExtentAffineSize(a)
	bSize, bSizeOK := optimizationExtentAffineSize(b)
	if aSizeOK && bSizeOK && optimizationAffineExprEqual(aSize, bSize) {
		return true
	}
	switch {
	case a.Kind == OptimizationExtentShape && b.Kind == OptimizationExtentShape:
		return SameShape(a.Shape, b.Shape)
	case a.Kind == OptimizationExtentArraySize && b.Kind == OptimizationExtentArraySize:
		if a.HasConstSize && b.HasConstSize {
			return a.ConstSize == b.ConstSize
		}
		return a.Size != "" && a.Size == b.Size
	case a.Kind == OptimizationExtentArraySize && b.Kind == OptimizationExtentViewBounds:
		if a.HasConstSize {
			if bSize, ok := optimizationExtentConstSize(b); ok {
				return a.ConstSize == bSize
			}
		}
		return a.Size != "" && a.Size == b.Size
	case a.Kind == OptimizationExtentViewBounds && b.Kind == OptimizationExtentArraySize:
		return SameOptimizationExtentSize(b, a)
	case a.Kind == OptimizationExtentViewBounds && b.Kind == OptimizationExtentViewBounds:
		if a.Size != "" && a.Size == b.Size {
			return true
		}
		aBegin, aBeginOK := optimizationConstInt(a.Begin)
		aEnd, aEndOK := optimizationConstInt(a.End)
		bBegin, bBeginOK := optimizationConstInt(b.Begin)
		bEnd, bEndOK := optimizationConstInt(b.End)
		if aBeginOK && aEndOK && bBeginOK && bEndOK {
			return (aEnd - aBegin) == (bEnd - bBegin)
		}
	}
	return false
}

func cloneOptimizationExtent(extent *OptimizationExtent) *OptimizationExtent {
	if extent == nil {
		return nil
	}
	cloned := *extent
	return &cloned
}

func optimizationExtentsDisjoint(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Kind != OptimizationExtentViewBounds || b.Kind != OptimizationExtentViewBounds {
		return false
	}
	if a.End != "" && a.End == b.Begin {
		return true
	}
	if b.End != "" && b.End == a.Begin {
		return true
	}
	if cmp, ok := optimizationExtentValueCompare(a.End, b.Begin, a.Size); ok && cmp <= 0 {
		return true
	}
	if cmp, ok := optimizationExtentValueCompare(b.End, a.Begin, b.Size); ok && cmp <= 0 {
		return true
	}
	if a.Size != "" && a.Size == b.Size {
		aBeginScale, aBeginBase, aBeginScaleOK := optimizationScaledExtentValue(a.Begin)
		aEndScale, aEndBase, aEndScaleOK := optimizationScaledExtentValue(a.End)
		bBeginScale, bBeginBase, bBeginScaleOK := optimizationScaledExtentValue(b.Begin)
		bEndScale, bEndBase, bEndScaleOK := optimizationScaledExtentValue(b.End)
		if aBeginScaleOK && aEndScaleOK && bBeginScaleOK && bEndScaleOK && aBeginBase == aEndBase && aBeginBase == bBeginBase && aBeginBase == bEndBase {
			if aEndScale <= bBeginScale || bEndScale <= aBeginScale {
				return true
			}
		}
	}
	aBegin, aBeginOK := optimizationConstInt(a.Begin)
	aEnd, aEndOK := optimizationConstInt(a.End)
	bBegin, bBeginOK := optimizationConstInt(b.Begin)
	bEnd, bEndOK := optimizationConstInt(b.End)
	if aEndOK && bBeginOK && aEnd <= bBegin {
		return true
	}
	if bEndOK && aBeginOK && bEnd <= aBegin {
		return true
	}
	return false
}

func optimizationExtentConstSize(extent *OptimizationExtent) (int64, bool) {
	if extent == nil {
		return 0, false
	}
	if extent.HasConstSize {
		return extent.ConstSize, true
	}
	if extent.Size != "" {
		if value, ok := optimizationConstInt(extent.Size); ok {
			return value, true
		}
	}
	if extent.Kind == OptimizationExtentViewBounds {
		begin, beginOK := optimizationConstInt(extent.Begin)
		end, endOK := optimizationConstInt(extent.End)
		if beginOK && endOK {
			return end - begin, true
		}
	}
	return 0, false
}

func newOptimizationAffineExpr() optimizationAffineExpr {
	return optimizationAffineExpr{Terms: map[string]int64{}}
}

func (e *optimizationAffineExpr) add(other optimizationAffineExpr) {
	if e == nil {
		return
	}
	e.Const += other.Const
	for term, coeff := range other.Terms {
		e.addTerm(term, coeff)
	}
}

func (e *optimizationAffineExpr) addTerm(term string, coeff int64) {
	if e == nil || term == "" || coeff == 0 {
		return
	}
	if e.Terms == nil {
		e.Terms = map[string]int64{}
	}
	e.Terms[term] += coeff
	if e.Terms[term] == 0 {
		delete(e.Terms, term)
	}
}

func (e optimizationAffineExpr) scaled(factor int64) optimizationAffineExpr {
	out := newOptimizationAffineExpr()
	out.Const = e.Const * factor
	for term, coeff := range e.Terms {
		out.Terms[term] = coeff * factor
	}
	return out
}

func (e optimizationAffineExpr) sub(other optimizationAffineExpr) optimizationAffineExpr {
	out := newOptimizationAffineExpr()
	out.Const = e.Const - other.Const
	for term, coeff := range e.Terms {
		out.Terms[term] = coeff
	}
	for term, coeff := range other.Terms {
		out.addTerm(term, -coeff)
	}
	return out
}

func optimizationAffineIsConst(expr optimizationAffineExpr) bool {
	return len(expr.Terms) == 0
}

func optimizationAffineExprTermsEqual(left optimizationAffineExpr, right optimizationAffineExpr) bool {
	if len(left.Terms) != len(right.Terms) {
		return false
	}
	for term, coeff := range left.Terms {
		if right.Terms[term] != coeff {
			return false
		}
	}
	return true
}

func optimizationAffineExprEqual(left optimizationAffineExpr, right optimizationAffineExpr) bool {
	return left.Const == right.Const && optimizationAffineExprTermsEqual(left, right)
}

func optimizationAffineCompare(left optimizationAffineExpr, right optimizationAffineExpr) (int, bool) {
	if !optimizationAffineExprTermsEqual(left, right) {
		return 0, false
	}
	switch {
	case left.Const < right.Const:
		return -1, true
	case left.Const > right.Const:
		return 1, true
	default:
		return 0, true
	}
}

func optimizationAffineCompareByPositiveSymbol(left optimizationAffineExpr, right optimizationAffineExpr, symbol string) (int, bool) {
	if symbol == "" {
		return 0, false
	}
	leftResidual := newOptimizationAffineExpr()
	leftResidual.Const = left.Const
	for term, coeff := range left.Terms {
		if term == symbol {
			continue
		}
		leftResidual.Terms[term] = coeff
	}
	rightResidual := newOptimizationAffineExpr()
	rightResidual.Const = right.Const
	for term, coeff := range right.Terms {
		if term == symbol {
			continue
		}
		rightResidual.Terms[term] = coeff
	}
	if !optimizationAffineExprEqual(leftResidual, rightResidual) {
		return 0, false
	}
	leftCoeff := left.Terms[symbol]
	rightCoeff := right.Terms[symbol]
	switch {
	case leftCoeff < rightCoeff:
		return -1, true
	case leftCoeff > rightCoeff:
		return 1, true
	default:
		return 0, true
	}
}

func optimizationAffineCompareBySinglePositiveTerm(left optimizationAffineExpr, right optimizationAffineExpr) (int, bool) {
	leftResidual := newOptimizationAffineExpr()
	rightResidual := newOptimizationAffineExpr()
	leftResidual.Const = left.Const
	rightResidual.Const = right.Const
	varyingTerm := ""
	seen := map[string]bool{}
	for term := range left.Terms {
		seen[term] = true
	}
	for term := range right.Terms {
		seen[term] = true
	}
	for term := range seen {
		leftCoeff := left.Terms[term]
		rightCoeff := right.Terms[term]
		if leftCoeff == rightCoeff {
			if leftCoeff != 0 {
				leftResidual.Terms[term] = leftCoeff
				rightResidual.Terms[term] = rightCoeff
			}
			continue
		}
		if varyingTerm != "" {
			return 0, false
		}
		varyingTerm = term
	}
	if varyingTerm == "" {
		return 0, false
	}
	if !optimizationAffineExprEqual(leftResidual, rightResidual) {
		return 0, false
	}
	return optimizationAffineCompareByPositiveSymbol(left, right, varyingTerm)
}

func optimizationExtentValueCompare(left string, right string, size string) (int, bool) {
	leftAffine, ok := parseOptimizationAffineExpr(left)
	if !ok {
		return 0, false
	}
	rightAffine, ok := parseOptimizationAffineExpr(right)
	if !ok {
		return 0, false
	}
	if cmp, ok := optimizationAffineCompare(leftAffine, rightAffine); ok {
		return cmp, true
	}
	if size != "" {
		if _, ok := optimizationConstInt(size); !ok {
			if cmp, ok := optimizationAffineCompareByPositiveSymbol(leftAffine, rightAffine, optimizationTrimExtentExpr(size)); ok {
				return cmp, true
			}
		}
	}
	if cmp, ok := optimizationAffineCompareBySinglePositiveTerm(leftAffine, rightAffine); ok {
		return cmp, true
	}
	return 0, false
}

func optimizationExtentAffineSize(extent *OptimizationExtent) (optimizationAffineExpr, bool) {
	if extent == nil {
		return optimizationAffineExpr{}, false
	}
	if extent.HasConstSize {
		out := newOptimizationAffineExpr()
		out.Const = extent.ConstSize
		return out, true
	}
	if extent.Size != "" {
		return parseOptimizationAffineExpr(extent.Size)
	}
	if extent.Kind != OptimizationExtentViewBounds {
		return optimizationAffineExpr{}, false
	}
	begin, beginOK := parseOptimizationAffineExpr(extent.Begin)
	end, endOK := parseOptimizationAffineExpr(extent.End)
	if !beginOK || !endOK {
		return optimizationAffineExpr{}, false
	}
	return end.sub(begin), true
}

func parseOptimizationAffineExpr(value string) (optimizationAffineExpr, bool) {
	trimmed := optimizationTrimExtentExpr(value)
	if trimmed == "" {
		return optimizationAffineExpr{}, false
	}
	if parts := optimizationSplitTopLevel(trimmed, " + "); len(parts) > 1 {
		out := newOptimizationAffineExpr()
		for _, part := range parts {
			parsed, ok := parseOptimizationAffineExpr(part)
			if !ok {
				return optimizationAffineExpr{}, false
			}
			out.add(parsed)
		}
		return out, true
	}
	if parts := optimizationSplitTopLevel(trimmed, " * "); len(parts) == 2 {
		left, leftOK := parseOptimizationAffineExpr(parts[0])
		right, rightOK := parseOptimizationAffineExpr(parts[1])
		if !leftOK || !rightOK {
			return optimizationAffineExpr{}, false
		}
		switch {
		case optimizationAffineIsConst(left):
			return right.scaled(left.Const), true
		case optimizationAffineIsConst(right):
			return left.scaled(right.Const), true
		default:
			return optimizationAffineExpr{}, false
		}
	}
	if constValue, ok := optimizationConstInt(trimmed); ok {
		out := newOptimizationAffineExpr()
		out.Const = constValue
		return out, true
	}
	out := newOptimizationAffineExpr()
	out.addTerm(trimmed, 1)
	return out, true
}

func optimizationTrimExtentExpr(value string) string {
	trimmed := value
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		depth := 0
		balanced := true
		for i := 0; i < len(trimmed); i++ {
			switch trimmed[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth < 0 {
					balanced = false
				}
				if depth == 0 && i != len(trimmed)-1 {
					balanced = false
				}
			}
		}
		if !balanced || depth != 0 {
			break
		}
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func optimizationSplitTopLevel(value string, sep string) []string {
	if value == "" || sep == "" || !strings.Contains(value, sep) {
		return nil
	}
	parts := make([]string, 0, 2)
	depth := 0
	start := 0
	found := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && i+len(sep) <= len(value) && value[i:i+len(sep)] == sep {
			parts = append(parts, value[start:i])
			start = i + len(sep)
			i += len(sep) - 1
			found = true
		}
	}
	if !found {
		return nil
	}
	parts = append(parts, value[start:])
	return parts
}

func optimizationScaledExtentValue(value string) (int64, string, bool) {
	trimmed := optimizationTrimExtentExpr(value)
	if trimmed == "" {
		return 0, "", false
	}
	if constValue, ok := optimizationConstInt(trimmed); ok {
		return constValue, "", true
	}
	sep := " * "
	sepIdx := -1
	for i := 0; i+len(sep) <= len(trimmed); i++ {
		if trimmed[i:i+len(sep)] == sep {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return 1, trimmed, true
	}
	left := trimmed[:sepIdx]
	right := trimmed[sepIdx+len(sep):]
	multiplier, ok := optimizationConstInt(left)
	if !ok {
		return 0, "", false
	}
	if right == "" {
		return 0, "", false
	}
	return multiplier, right, true
}

func optimizationChunkExtentOffsetExpr(chunkSize string, chunkIndex int64) string {
	if chunkIndex <= 0 {
		return "0"
	}
	if chunkIndex == 1 {
		return chunkSize
	}
	return fmt.Sprintf("(%d * %s)", chunkIndex, chunkSize)
}

func optimizationAddOffsetExpr(base string, offset string) string {
	if base == "" || base == "0" {
		return offset
	}
	if offset == "" || offset == "0" {
		return base
	}
	if baseValue, baseOK := optimizationConstInt(base); baseOK {
		if offsetValue, offsetOK := optimizationConstInt(offset); offsetOK {
			return fmt.Sprintf("%d", baseValue+offsetValue)
		}
	}
	return fmt.Sprintf("(%s + %s)", base, offset)
}

func composeOptimizationExtentWithExactBase(base *OptimizationExtent, begin string, end string) *OptimizationExtent {
	if begin == "" || end == "" {
		return nil
	}
	if base == nil || base.Kind != OptimizationExtentViewBounds {
		return &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: end}
	}
	baseBegin := base.Begin
	if baseBegin == "" {
		baseBegin = "0"
	}
	return &OptimizationExtent{
		Kind:  OptimizationExtentViewBounds,
		Begin: optimizationAddOffsetExpr(baseBegin, begin),
		End:   optimizationAddOffsetExpr(baseBegin, end),
	}
}

func optimizationConstInt(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	trimmed := value
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	if last := trimmed[len(trimmed)-1]; last == 'u' || last == 'i' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	value64, err := parseOptimizationInt(trimmed)
	if err != nil {
		return 0, false
	}
	return value64, true
}

func parseOptimizationInt(value string) (int64, error) {
	negative := false
	if value != "" && value[0] == '-' {
		negative = true
		value = value[1:]
	}
	if value == "" {
		return 0, fmt.Errorf("empty int")
	}
	var out int64
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-decimal int %q", value)
		}
		out = out*10 + int64(ch-'0')
	}
	if negative {
		out = -out
	}
	return out, nil
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
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if tt.SurfaceName == "packedview" || tt.SurfaceName == "packedtags" {
			facts.ReadOnly = true
		}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
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
		if _, ok := ChunksExactViewInstance(tt); ok {
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
	return a.inferExprOptimizationFactsWithBase(expr, t, optimizationFactsForType(t))
}

func (a *Analyzer) inferExprOptimizationFactsWithBase(expr ast.Expr, t Type, facts OptimizationFacts) OptimizationFacts {
	if expr == nil {
		return facts
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if identFacts, ok := a.lookupIdentOptimizationFacts(n); ok {
			facts = identFacts
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n)
		}
	case *ast.IndexExpr:
		if resolved, ok := a.resolveIndexedValueExpr(n.Object, n.Index); ok {
			if resolvedFacts, ok := a.exprFacts[resolved]; ok {
				facts = resolvedFacts
				facts.Exclusive = false
			}
			if facts.base == "" {
				facts.base = a.optimizationBaseForExpr(resolved)
			}
		}
		if chunkFacts, ok := a.inferChunksExactItemOptimizationFacts(n); ok {
			facts = overlayOptimizationFacts(facts, chunkFacts)
		}
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			if resolvedFacts, ok := a.exprFacts[resolved]; ok {
				facts = resolvedFacts
				facts.Exclusive = false
			}
		}
		if splitFacts, ok := a.inferSplitViewFieldOptimizationFacts(n); ok {
			facts = overlayOptimizationFacts(facts, splitFacts)
		}
		if n.Field == "values" {
			if tableInfo, ok := a.nodeTableInfoForExpr(n.Object); ok && tableInfo.Enum != nil {
				if objectFacts, ok := a.exprFacts[n.Object]; ok && objectFacts.Exclusive {
					facts.Exclusive = true
				}
				facts.ReadOnly = false
				facts.Contiguous = true
				facts.UnitStride = true
				if base := a.optimizationBaseForExpr(n.Object); base != "" {
					facts.base = base + ".values"
				} else {
					facts.base = optimizationExprString(expr)
				}
				if tableInfo.CountExpr != "" {
					facts.Extent = &OptimizationExtent{
						Kind:  OptimizationExtentViewBounds,
						Begin: "0",
						End:   tableInfo.CountExpr,
					}
				}
				facts.PackedStoreProvenance = PackedStoreProvenance{
					HasPackedStoreDeps:       true,
					HasFrozenPackedStoreDeps: true,
				}
				facts.FrozenPackedStoreOnly = true
			}
		}
		if n.Field == "tags" {
			if storeType, ok := a.exprTypes[n.Object].(*PackedEnumStoreType); ok && IsFrozenPackedEnumStoreType(storeType) {
				facts.ReadOnly = true
				facts.Contiguous = true
				facts.UnitStride = true
				facts.base = optimizationExprString(expr)
				facts.Extent = &OptimizationExtent{
					Kind:  OptimizationExtentViewBounds,
					Begin: "0",
					End:   optimizationExprString(&ast.FieldExpr{Position: n.Position, Object: n.Object, Field: "count"}),
				}
			}
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n.Object)
		}
	case *ast.CallExpr:
		facts = a.inferCallOptimizationFacts(n, facts)
	case *ast.AllocExpr:
		if _, ok := t.(*RefType); ok {
			facts.Exclusive = true
			facts.base = optimizationExprIdentity(expr)
		}
	case *ast.StringLit:
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
	case *ast.SliceExpr:
		if objectFacts, ok := a.lookupOptimizationFactsForExpr(n.Object); ok {
			if facts.base == "" {
				facts.base = objectFacts.base
				if facts.base == "" {
					facts.base = a.optimizationBaseForExpr(n.Object)
				}
			}
			facts.ReadOnly = facts.ReadOnly || objectFacts.ReadOnly
			facts.Contiguous = facts.Contiguous || objectFacts.Contiguous
			facts.UnitStride = facts.UnitStride || objectFacts.UnitStride
		}
		if field := a.sliceFullSpanField(n.Object); field != "" {
			if extent := a.inferRelativeOptimizationExtent(n.Object, n.Start, n.End, field); extent != nil {
				facts.Extent = extent
			}
		} else if fieldExpr, ok := stripOptimizationParens(n.Object).(*ast.FieldExpr); ok && fieldExpr.Field == "tags" {
			if extent := a.inferRelativeOptimizationExtent(fieldExpr.Object, n.Start, n.End, "count"); extent != nil {
				facts.Extent = extent
			}
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n.Object)
		}
	case *ast.TernaryExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Alt, facts)
	case *ast.TryExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Fallback, facts)
	case *ast.UnwrapElseExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Fallback, facts)
	}
	if typeMayCarryRegionProvenanceForOptimization(t) {
		if provenance, ok := a.exprPackedStoreProvenance(expr); ok {
			facts.PackedStoreProvenance = provenance
			facts.FrozenPackedStoreOnly = provenance.DependsOnlyOnFrozenPackedStores()
		}
	}
	return facts
}

func exprRequiresOptimizationFactInference(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident, *ast.IndexExpr, *ast.FieldExpr, *ast.CallExpr, *ast.AllocExpr, *ast.SliceExpr, *ast.TernaryExpr, *ast.TryExpr, *ast.UnwrapElseExpr:
		return true
	default:
		return false
	}
}

func typeMayCarryRegionProvenanceForOptimization(t Type) bool {
	switch tt := t.(type) {
	case nil:
		return false
	case *InvalidType, *NeverType, *NullType, *BuiltinType, *RefStorageParamType, *RefStorageValueType, *RefStateParamType, *RefStateValueType, *ErrorSetType, *ConstEnumType, *FuncType, *OpaqueType:
		return false
	case *ErrorUnionType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *OptionalType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *ArrayType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Elem)
	case *DictType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Key) || typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *AggregateStateType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Base)
	default:
		return true
	}
}

func (a *Analyzer) inferRecoveredExprOptimizationFacts(value ast.Expr, fallback ast.Expr, facts OptimizationFacts) OptimizationFacts {
	if a == nil {
		return facts
	}
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if valueFacts, ok := a.exprFacts[value]; ok {
			facts = overlayOptimizationFacts(facts, valueFacts)
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(value)
		}
		return facts
	}
	valueFacts, valueOK := a.exprFacts[value]
	fallbackFacts, fallbackOK := a.exprFacts[fallback]
	if valueOK && fallbackOK {
		if merged, ok := mergeAlternativeOptimizationFacts(valueFacts, fallbackFacts); ok {
			facts = overlayOptimizationFacts(facts, merged)
		}
	}
	if facts.base == "" {
		facts.base = a.sharedOptimizationBaseForExprs(value, fallback)
	}
	return facts
}

func overlayOptimizationFacts(dst OptimizationFacts, src OptimizationFacts) OptimizationFacts {
	if src.Exclusive {
		dst.Exclusive = true
	}
	if src.ReadOnly {
		dst.ReadOnly = true
	}
	if src.Contiguous {
		dst.Contiguous = true
	}
	if src.UnitStride {
		dst.UnitStride = true
	}
	if src.Extent != nil {
		dst.Extent = cloneOptimizationExtent(src.Extent)
	}
	if src.base != "" {
		dst.base = src.base
	}
	return dst
}

func mergeAlternativeOptimizationFacts(left OptimizationFacts, right OptimizationFacts) (OptimizationFacts, bool) {
	merged := OptimizationFacts{}
	if left.base != "" && left.base == right.base {
		merged.base = left.base
	}
	merged.ReadOnly = left.ReadOnly || right.ReadOnly
	merged.Contiguous = left.Contiguous && right.Contiguous
	merged.UnitStride = left.UnitStride && right.UnitStride
	if SameOptimizationExtent(left.Extent, right.Extent) {
		merged.Extent = cloneOptimizationExtent(left.Extent)
		if merged.base != "" && left.Exclusive && right.Exclusive {
			merged.Exclusive = true
		}
	}
	if !merged.Exclusive && !merged.ReadOnly && !merged.Contiguous && !merged.UnitStride && merged.Extent == nil && merged.base == "" {
		return OptimizationFacts{}, false
	}
	return merged, true
}

func summarizePackedStoreProvenance(state regionRefState) PackedStoreProvenance {
	if state.PackedStoreSummaryKnown {
		return state.PackedStoreSummary
	}
	var out PackedStoreProvenance
	summarizePackedStoreProvenanceInto(&out, state)
	return out
}

func mergePackedStoreProvenanceInto(dst *PackedStoreProvenance, src PackedStoreProvenance) {
	if dst == nil {
		return
	}
	dst.HasPackedStoreDeps = dst.HasPackedStoreDeps || src.HasPackedStoreDeps
	dst.HasFrozenPackedStoreDeps = dst.HasFrozenPackedStoreDeps || src.HasFrozenPackedStoreDeps
	dst.HasNonFrozenPackedStoreDeps = dst.HasNonFrozenPackedStoreDeps || src.HasNonFrozenPackedStoreDeps
	dst.HasNonStoreProvenance = dst.HasNonStoreProvenance || src.HasNonStoreProvenance
}

func summarizePackedStoreProvenanceInto(out *PackedStoreProvenance, state regionRefState) {
	if out == nil {
		return
	}
	if state.PackedStoreSummaryKnown {
		mergePackedStoreProvenanceInto(out, state.PackedStoreSummary)
		return
	}
	if len(state.Deps) != 0 || hasRegionParamDependencies(state) {
		out.HasNonStoreProvenance = true
	}
	for _, dep := range state.StoreDeps {
		out.HasPackedStoreDeps = true
		if dep.Type != nil && IsFrozenPackedEnumStoreType(dep.Type) {
			out.HasFrozenPackedStoreDeps = true
			continue
		}
		out.HasNonFrozenPackedStoreDeps = true
	}
	for _, fieldState := range state.Fields {
		summarizePackedStoreProvenanceInto(out, fieldState)
	}
}

func (a *Analyzer) exprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	if a == nil || expr == nil {
		return PackedStoreProvenance{}, false
	}
	state, ok := a.regionRefStateForExpr(expr)
	if !ok {
		return PackedStoreProvenance{}, false
	}
	return summarizePackedStoreProvenance(state), true
}

func (a *Analyzer) exprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	provenance, ok := a.exprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnlyOnFrozenPackedStores()
}

func regionRefStateDependsOnlyOnFrozenPackedStores(state regionRefState) (bool, bool) {
	if state.PackedStoreSummaryKnown {
		summary := state.PackedStoreSummary
		return summary.DependsOnlyOnFrozenPackedStores() || (!summary.HasAnyPackedStoreProvenance() && !summary.HasNonStoreProvenance), summary.DependsOnFrozenPackedStores()
	}
	if len(state.Deps) != 0 || hasRegionParamDependencies(state) {
		return false, false
	}
	hasFrozen := false
	for _, dep := range state.StoreDeps {
		if dep.Type == nil || !IsFrozenPackedEnumStoreType(dep.Type) {
			return false, false
		}
		hasFrozen = true
	}
	for _, fieldState := range state.Fields {
		fieldOnlyFrozen, fieldHasFrozen := regionRefStateDependsOnlyOnFrozenPackedStores(fieldState)
		if !fieldOnlyFrozen {
			return false, false
		}
		hasFrozen = hasFrozen || fieldHasFrozen
	}
	return true, hasFrozen
}

func (a *Analyzer) sliceFullSpanField(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	t := a.exprTypes[expr]
	for {
		ref, ok := t.(*RefType)
		if !ok {
			break
		}
		t = ref.Elem
	}
	switch tt := t.(type) {
	case *DArrayType:
		return "count"
	case *DArrayViewType:
		return "len"
	case *DStrType, *SViewType:
		return "len"
	case *StructType:
		if tt != nil {
			if _, ok := dynArrayViewRuntimeType(tt); ok {
				return "len"
			}
			if _, ok := stringViewRuntimeType(tt); ok {
				return "len"
			}
		}
	}
	return ""
}

func (a *Analyzer) recordImmutableSymbolOptimizationFacts(sym *Symbol, expr ast.Expr) {
	if a == nil || sym == nil || expr == nil || sym.Mutable || a.symbolFacts == nil {
		return
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return
	}
	facts.Exclusive = false
	a.symbolFacts[sym] = facts
}

func (a *Analyzer) lookupIdentOptimizationFacts(ident *ast.Ident) (OptimizationFacts, bool) {
	if a == nil || ident == nil || a.symbolFacts == nil {
		return OptimizationFacts{}, false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(ident.Name); ok {
			if facts, ok := a.symbolFacts[sym]; ok {
				return facts, true
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if facts, ok := a.symbolFacts[root]; ok {
					return facts, true
				}
			}
		}
	}
	if a.globalScope != nil {
		if sym, ok := a.globalScope.Lookup(ident.Name); ok {
			if facts, ok := a.symbolFacts[sym]; ok {
				return facts, true
			}
		}
	}
	return OptimizationFacts{}, false
}

func (a *Analyzer) inferCallOptimizationFacts(call *ast.CallExpr, facts OptimizationFacts) OptimizationFacts {
	if call == nil {
		return facts
	}
	switch optimizationHelperName(call.Func) {
	case "readonly":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts = overlayOptimizationFacts(facts, sourceFacts)
		}
		facts.ReadOnly = true
		facts.Exclusive = false
	case "split_at":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts.base = sourceFacts.base
			facts.ReadOnly = sourceFacts.ReadOnly
			facts.Contiguous = sourceFacts.Contiguous
			facts.UnitStride = sourceFacts.UnitStride
			facts.Exclusive = false
		}
	case "chunks_exact":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts.base = sourceFacts.base
			facts.ReadOnly = sourceFacts.ReadOnly
			facts.Contiguous = sourceFacts.Contiguous
			facts.UnitStride = sourceFacts.UnitStride
		}
		if callName := optimizationExprString(call); callName != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: callName + ".len"}
		}
		facts.Exclusive = false
	case "arena_da_view", "ctx_string_view", "string_view":
		if baseExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(baseExpr)
			if baseFacts, ok := a.exprFacts[baseExpr]; ok && baseFacts.Exclusive {
				facts.Exclusive = true
			}
		}
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "count"); extent != nil {
			facts.Extent = extent
		}
	case "arena_da_view_prefix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if endExpr, ok := optimizationCallArg(call, 1); ok {
					zeroExpr := &ast.IntLit{Position: call.Position, Value: "0"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, zeroExpr, endExpr, "len")
				}
			}
		}
	case "arena_da_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					endExpr := &ast.FieldExpr{Position: call.Position, Object: viewExpr, Field: "len"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, startExpr, endExpr, "len")
				}
			}
		}
	case "string_view_prefix", "ctx_string_view_prefix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if endExpr, ok := optimizationCallArg(call, 1); ok {
					zeroExpr := &ast.IntLit{Position: call.Position, Value: "0"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, zeroExpr, endExpr, "len")
				}
			}
		}
	case "string_view_suffix", "ctx_string_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					endExpr := &ast.FieldExpr{Position: call.Position, Object: viewExpr, Field: "len"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, startExpr, endExpr, "len")
				}
			}
		}
	case "arena_da_view_slice", "ctx_string_view_slice", "string_view_slice":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.exprFacts[viewExpr]; ok && viewFacts.Exclusive {
				facts.Exclusive = true
			}
		}
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "len"); extent != nil {
			facts.Extent = extent
		}
	case "arena_da_from_view":
		if viewFacts, ok := a.exprFactsForCallArg(call, 1); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	case "ctx_string_from_view":
		if viewFacts, ok := a.exprFactsForCallArg(call, 0); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	case "ctx_string_slice":
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "len"); extent != nil {
			facts.Extent = extent
		}
	case "string_view_copy":
		if viewFacts, ok := a.exprFactsForCallArg(call, 0); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	}
	return facts
}

func (a *Analyzer) optimizationBaseForExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	stripped := stripOptimizationParens(expr)
	switch n := stripped.(type) {
	case *ast.Ident:
		if sym := a.lookupOptimizationSymbol(n); sym != nil {
			if a != nil && a.symbolFacts != nil {
				if facts, ok := a.symbolFacts[sym]; ok && facts.base != "" {
					return facts.base
				}
			}
			return optimizationSymbolIdentity(sym)
		}
	case *ast.AllocExpr:
		return optimizationExprIdentity(n)
	case *ast.IndexExpr:
		if resolved, ok := a.resolveIndexedValueExpr(n.Object, n.Index); ok {
			return a.optimizationBaseForExpr(resolved)
		}
	case *ast.MoveExpr:
		return a.optimizationBaseForExpr(n.Operand)
	case *ast.CanExpr:
		return a.optimizationBaseForExpr(n.Expr)
	case *ast.TernaryExpr:
		return a.sharedOptimizationBaseForExprs(n.Value, n.Alt)
	case *ast.TryExpr:
		if n.Fallback == nil || a.exprDefinitelyNever(n.Fallback) {
			return a.optimizationBaseForExpr(n.Value)
		}
		return a.sharedOptimizationBaseForExprs(n.Value, n.Fallback)
	case *ast.UnwrapElseExpr:
		if a.exprDefinitelyNever(n.Fallback) {
			return a.optimizationBaseForExpr(n.Value)
		}
		return a.sharedOptimizationBaseForExprs(n.Value, n.Fallback)
	}
	if a != nil && a.exprFacts != nil {
		if facts, ok := a.exprFacts[stripped]; ok && facts.base != "" {
			return facts.base
		}
	}
	return ""
}

func (a *Analyzer) sharedOptimizationBaseForExprs(left ast.Expr, right ast.Expr) string {
	leftBase := a.optimizationBaseForExpr(left)
	if leftBase == "" || right == nil {
		return leftBase
	}
	rightBase := a.optimizationBaseForExpr(right)
	if leftBase == rightBase {
		return leftBase
	}
	return ""
}

func (a *Analyzer) lookupOptimizationSymbol(ident *ast.Ident) *Symbol {
	if a == nil || ident == nil {
		return nil
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(ident.Name); ok {
			return sym
		}
	}
	if a.globalScope != nil {
		if sym, ok := a.globalScope.Lookup(ident.Name); ok {
			return sym
		}
	}
	return nil
}

func optimizationSymbolIdentity(sym *Symbol) string {
	if sym == nil {
		return ""
	}
	return fmt.Sprintf("symbol@%p", sym)
}

func optimizationExprIdentity(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	return fmt.Sprintf("expr@%p", expr)
}

func (a *Analyzer) inferViewHelperExtent(call *ast.CallExpr, baseIndex, startIndex, endIndex int, fullSpanField string) *OptimizationExtent {
	baseExpr, ok := optimizationCallArg(call, baseIndex)
	if !ok {
		return nil
	}
	startExpr, hasStart := optimizationCallArg(call, startIndex)
	endExpr, hasEnd := optimizationCallArg(call, endIndex)
	if !hasStart || !hasEnd {
		return nil
	}
	return a.inferRelativeOptimizationExtent(baseExpr, startExpr, endExpr, fullSpanField)
}

func (a *Analyzer) inferRelativeOptimizationExtent(baseExpr ast.Expr, startExpr ast.Expr, endExpr ast.Expr, fullSpanField string) *OptimizationExtent {
	if a == nil || baseExpr == nil || startExpr == nil || endExpr == nil {
		return nil
	}
	begin := optimizationExprString(startExpr)
	end := optimizationExprString(endExpr)
	if begin == "" || end == "" {
		return nil
	}
	baseFacts, ok := a.lookupOptimizationFactsForExpr(baseExpr)
	if ok && baseFacts.HasExactExtent() {
		if isZeroOptimizationExpr(startExpr) && optimizationFieldMatches(endExpr, baseExpr, fullSpanField) {
			return cloneOptimizationExtent(baseFacts.Extent)
		}
		if optimizationFieldMatches(endExpr, baseExpr, fullSpanField) && baseFacts.Extent != nil && baseFacts.Extent.Kind == OptimizationExtentViewBounds {
			composed := composeOptimizationExtentWithExactBase(baseFacts.Extent, begin, end)
			if composed != nil {
				composed.End = baseFacts.Extent.End
			}
			return composed
		}
		return composeOptimizationExtentWithExactBase(baseFacts.Extent, begin, end)
	}
	return &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: end}
}

func optimizationHelperName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.ParenExpr:
		return optimizationHelperName(n.Inner)
	case *ast.SpecializeExpr:
		return optimizationHelperName(n.Operand)
	default:
		return ""
	}
}

func (a *Analyzer) exprFactsForCallArg(call *ast.CallExpr, index int) (OptimizationFacts, bool) {
	expr, ok := optimizationCallArg(call, index)
	if !ok {
		return OptimizationFacts{}, false
	}
	return a.lookupOptimizationFactsForExpr(expr)
}

func (a *Analyzer) lookupOptimizationFactsForExpr(expr ast.Expr) (OptimizationFacts, bool) {
	if a == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	if facts, ok := a.exprFacts[expr]; ok {
		return facts, true
	}
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok {
		return OptimizationFacts{}, false
	}
	return a.lookupIdentOptimizationFacts(ident)
}

func (a *Analyzer) boundCallExpr(expr ast.Expr) (*ast.CallExpr, bool) {
	if expr == nil {
		return nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.boundCallExpr(n.Inner)
	case *ast.CastExpr:
		return a.boundCallExpr(n.Operand)
	case *ast.MoveExpr:
		return a.boundCallExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym.Mutable {
			return nil, false
		}
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.boundCallExpr(valueExpr)
			}
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl.Value == nil {
			return nil, false
		}
		return a.boundCallExpr(decl.Value)
	case *ast.CallExpr:
		return n, true
	default:
		return nil, false
	}
}

func (a *Analyzer) inferSplitViewFieldOptimizationFacts(expr *ast.FieldExpr) (OptimizationFacts, bool) {
	if a == nil || expr == nil || expr.Field == "" {
		return OptimizationFacts{}, false
	}
	call, ok := a.boundCallExpr(expr.Object)
	if !ok || callIdentName(call) != "split_at" || len(call.Args) < 2 {
		return OptimizationFacts{}, false
	}
	if expr.Field != "left" && expr.Field != "right" {
		return OptimizationFacts{}, false
	}
	baseFacts, ok := a.lookupOptimizationFactsForExpr(call.Args[0])
	if !ok {
		return OptimizationFacts{}, false
	}
	facts := baseFacts
	if facts.base == "" {
		facts.base = a.optimizationBaseForExpr(call.Args[0])
	}
	if facts.Extent != nil {
		index := optimizationExprString(call.Args[1])
		if index != "" {
			begin := "0"
			end := optimizationExprString(&ast.FieldExpr{Position: call.Position, Object: call.Args[0], Field: "len"})
			if facts.Extent.Kind == OptimizationExtentViewBounds {
				if facts.Extent.Begin != "" {
					begin = facts.Extent.Begin
				}
				if facts.Extent.End != "" {
					end = facts.Extent.End
				}
			}
			if expr.Field == "right" {
				facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: optimizationAddOffsetExpr(begin, index), End: end}
			} else {
				facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: optimizationAddOffsetExpr(begin, index)}
			}
			if expr.Field == "left" && optimizationFieldMatches(call.Args[1], call.Args[0], "len") {
				facts.Extent = cloneOptimizationExtent(baseFacts.Extent)
			}
			if expr.Field == "right" && isZeroOptimizationExpr(call.Args[1]) {
				facts.Extent = cloneOptimizationExtent(baseFacts.Extent)
			}
		}
	}
	return facts, true
}

func (a *Analyzer) inferChunksExactItemOptimizationFacts(expr *ast.IndexExpr) (OptimizationFacts, bool) {
	if a == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	objectType := a.exprTypes[expr.Object]
	if _, ok := ChunksExactViewItemType(objectType); !ok {
		return OptimizationFacts{}, false
	}
	objectFacts, ok := a.exprFacts[expr.Object]
	if !ok {
		return OptimizationFacts{}, false
	}
	facts := objectFacts
	facts.Exclusive = false
	if facts.base == "" {
		facts.base = a.optimizationBaseForExpr(expr.Object)
	}
	chunkSize := ""
	chunkSizeConst, chunkSizeConstOK := int64(0), false
	if call, ok := a.boundCallExpr(expr.Object); ok && callIdentName(call) == "chunks_exact" && len(call.Args) >= 2 {
		chunkSize = optimizationExprString(call.Args[1])
		chunkSizeConst, chunkSizeConstOK = a.resolveProjectedFieldConstIntExpr(call.Args[1])
	}
	if chunkSize == "" {
		chunkSize = optimizationExprString(&ast.FieldExpr{Position: expr.Position, Object: expr.Object, Field: "chunk_size"})
	}
	chunkIndex, chunkIndexOK := a.resolveProjectedFieldConstIntExpr(expr.Index)
	sourceBegin := "0"
	if call, ok := a.boundCallExpr(expr.Object); ok && callIdentName(call) == "chunks_exact" && len(call.Args) >= 1 {
		if sourceFacts, ok := a.lookupOptimizationFactsForExpr(call.Args[0]); ok && sourceFacts.Extent != nil && sourceFacts.Extent.Kind == OptimizationExtentViewBounds && sourceFacts.Extent.Begin != "" {
			sourceBegin = sourceFacts.Extent.Begin
		}
	}
	if chunkSize != "" {
		switch {
		case chunkIndexOK && chunkSizeConstOK:
			offset := fmt.Sprintf("%d", chunkIndex*chunkSizeConst)
			begin := optimizationAddOffsetExpr(sourceBegin, offset)
			end := optimizationAddOffsetExpr(begin, fmt.Sprintf("%d", chunkSizeConst))
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Size: chunkSize, Begin: begin, End: end}
		case chunkIndexOK:
			begin := optimizationAddOffsetExpr(sourceBegin, optimizationChunkExtentOffsetExpr(chunkSize, chunkIndex))
			end := optimizationAddOffsetExpr(sourceBegin, optimizationChunkExtentOffsetExpr(chunkSize, chunkIndex+1))
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Size: chunkSize, Begin: begin, End: end}
		default:
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentArraySize, Size: chunkSize, HasConstSize: chunkSizeConstOK, ConstSize: chunkSizeConst}
		}
	}
	return facts, true
}

func optimizationCallArg(call *ast.CallExpr, index int) (ast.Expr, bool) {
	if call == nil || index < 0 || index >= len(call.Args) {
		return nil, false
	}
	return call.Args[index], true
}

func optimizationFieldMatches(expr ast.Expr, object ast.Expr, field string) bool {
	fieldExpr, ok := stripOptimizationParens(expr).(*ast.FieldExpr)
	if !ok || fieldExpr.Field != field {
		return false
	}
	return optimizationExprString(fieldExpr.Object) == optimizationExprString(object)
}

func isZeroOptimizationExpr(expr ast.Expr) bool {
	intLit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	if !ok {
		return false
	}
	return intLit.Value == "0"
}

func stripOptimizationParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.Inner
	}
}

func optimizationExprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		value := n.Value
		if n.Suffix != "" {
			value += n.Suffix
		}
		return value
	case *ast.StringLit:
		return fmt.Sprintf("%q", n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", optimizationExprString(n.Left), lexer.TokenName(n.Op), optimizationExprString(n.Right))
	case *ast.UnaryExpr:
		return fmt.Sprintf("(%s %s)", lexer.TokenName(n.Op), optimizationExprString(n.Operand))
	case *ast.MoveExpr:
		return fmt.Sprintf("move %s", optimizationExprString(n.Operand))
	case *ast.CallExpr:
		args := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			if name := n.ArgName(i); name != "" {
				args = append(args, name+": "+optimizationExprString(arg))
				continue
			}
			args = append(args, optimizationExprString(arg))
		}
		return fmt.Sprintf("%s(%s)", optimizationExprString(n.Func), joinOptimizationStrings(args))
	case *ast.FieldExpr:
		return fmt.Sprintf("%s.%s", optimizationExprString(n.Object), n.Field)
	case *ast.IndexExpr:
		return fmt.Sprintf("%s[%s]", optimizationExprString(n.Object), optimizationExprString(n.Index))
	case *ast.SliceExpr:
		return fmt.Sprintf("%s[%s:%s]", optimizationExprString(n.Object), optimizationExprString(n.Start), optimizationExprString(n.End))
	case *ast.ListLitExpr:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, optimizationExprString(elem))
		}
		return fmt.Sprintf("[%s]", joinOptimizationStrings(parts))
	case *ast.CastExpr:
		return fmt.Sprintf("%s.cast", optimizationExprString(n.Operand))
	case *ast.SizeofExpr:
		return "sizeof(...)"
	case *ast.TernaryExpr:
		return fmt.Sprintf("(%s if %s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Cond), optimizationExprString(n.Alt))
	case *ast.AddrOfExpr:
		return fmt.Sprintf("&%s", optimizationExprString(n.Operand))
	case *ast.SpecializeExpr:
		return optimizationExprString(n.Operand) + ".specialize"
	case *ast.StructLitExpr:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, optimizationExprString(arg))
		}
		return fmt.Sprintf("%s(%s)", n.Name, joinOptimizationStrings(parts))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", optimizationExprString(n.Inner))
	case *ast.AllocExpr:
		if n.Owner == nil {
			return fmt.Sprintf("new %s", optimizationExprString(n.Value))
		}
		return fmt.Sprintf("new[%s] %s", optimizationExprString(n.Owner), optimizationExprString(n.Value))
	case *ast.CanExpr:
		return optimizationExprString(n.Expr)
	case *ast.TryExpr:
		if n.Fallback == nil {
			return fmt.Sprintf("try %s", optimizationExprString(n.Value))
		}
		return fmt.Sprintf("(try %s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Fallback))
	case *ast.UnwrapElseExpr:
		return fmt.Sprintf("(%s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Fallback))
	default:
		return ""
	}
}

func joinOptimizationStrings(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}

func (r *Result) ExprOptimizationFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if r == nil || expr == nil || r.ExprFacts == nil {
		return OptimizationFacts{}, false
	}
	facts, ok := r.ExprFacts[expr]
	return facts, ok
}

func (r *Result) ExprsHaveSameExtent(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtent(rightFacts)
}

func (r *Result) ExprsHaveEqualExtentSize(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtentSize(rightFacts)
}

func (r *Result) ExprsAreDisjoint(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.Disjoint(rightFacts)
}

func (r *Result) ExprSupportsDenseWrite(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	facts, ok := r.ExprOptimizationFacts(expr)
	if !ok {
		return false
	}
	return facts.Contiguous && facts.UnitStride && !facts.ReadOnly
}

func (r *Result) ExprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	if r == nil {
		return PackedStoreProvenance{}, false
	}
	facts, ok := r.ExprOptimizationFacts(expr)
	if !ok {
		return PackedStoreProvenance{}, false
	}
	if !facts.PackedStoreProvenance.HasAnyPackedStoreProvenance() {
		return PackedStoreProvenance{}, false
	}
	return facts.PackedStoreProvenance, true
}

func (r *Result) ExprHasPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasAnyPackedStoreProvenance()
}

func (r *Result) ExprDependsOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnFrozenPackedStores()
}

func (r *Result) ExprDependsOnNonFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnNonFrozenPackedStores()
}

func (r *Result) ExprHasOnlyFrozenPackedStoreDeps(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasOnlyFrozenPackedStoreDeps()
}

func (r *Result) ExprHasMixedPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasMixedProvenance()
}

func (r *Result) ExprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnlyOnFrozenPackedStores()
}
