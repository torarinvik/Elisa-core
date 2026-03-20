package semantic

import (
	"fmt"

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
	Exclusive  bool
	ReadOnly   bool
	Contiguous bool
	UnitStride bool
	Extent     *OptimizationExtent
	base       string
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

func (f OptimizationFacts) Disjoint(other OptimizationFacts) bool {
	return OptimizationFactsDisjoint(f, other)
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
	switch n := expr.(type) {
	case *ast.Ident:
		if identFacts, ok := a.lookupIdentOptimizationFacts(n); ok {
			facts = identFacts
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n)
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
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n.Object)
		}
	}
	return facts
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
			if viewFacts, ok := a.exprFacts[viewExpr]; ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
				if endExpr, ok := optimizationCallArg(call, 1); ok && optimizationFieldMatches(endExpr, viewExpr, "len") && viewFacts.HasExactExtent() {
					facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
				}
			}
		}
		if facts.Extent == nil {
			if endExpr, ok := optimizationCallArg(call, 1); ok {
				if end := optimizationExprString(endExpr); end != "" {
					facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: end}
				}
			}
		}
	case "arena_da_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.exprFacts[viewExpr]; ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
				if startExpr, ok := optimizationCallArg(call, 1); ok && isZeroOptimizationExpr(startExpr) && viewFacts.HasExactExtent() {
					facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					begin := optimizationExprString(startExpr)
					viewName := optimizationExprString(viewExpr)
					if begin != "" && viewName != "" {
						facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: viewName + ".len"}
					}
				}
			}
		}
	case "string_view_prefix", "ctx_string_view_prefix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.exprFacts[viewExpr]; ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
				if endExpr, ok := optimizationCallArg(call, 1); ok && optimizationFieldMatches(endExpr, viewExpr, "len") && viewFacts.HasExactExtent() {
					facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
				}
			}
		}
		if facts.Extent == nil {
			if endExpr, ok := optimizationCallArg(call, 1); ok {
				if end := optimizationExprString(endExpr); end != "" {
					facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: end}
				}
			}
		}
	case "string_view_suffix", "ctx_string_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.exprFacts[viewExpr]; ok {
				if viewFacts.Exclusive {
					facts.Exclusive = true
				}
				if startExpr, ok := optimizationCallArg(call, 1); ok && isZeroOptimizationExpr(startExpr) && viewFacts.HasExactExtent() {
					facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
				}
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					begin := optimizationExprString(startExpr)
					viewName := optimizationExprString(viewExpr)
					if begin != "" && viewName != "" {
						facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: viewName + ".len"}
					}
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
	case *ast.MoveExpr:
		return a.optimizationBaseForExpr(n.Operand)
	case *ast.CanExpr:
		return a.optimizationBaseForExpr(n.Expr)
	}
	if a != nil && a.exprFacts != nil {
		if facts, ok := a.exprFacts[stripped]; ok && facts.base != "" {
			return facts.base
		}
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
	baseFacts, ok := a.exprFacts[baseExpr]
	if ok && baseFacts.HasExactExtent() {
		startExpr, hasStart := optimizationCallArg(call, startIndex)
		endExpr, hasEnd := optimizationCallArg(call, endIndex)
		if hasStart && hasEnd && isZeroOptimizationExpr(startExpr) && optimizationFieldMatches(endExpr, baseExpr, fullSpanField) {
			return cloneOptimizationExtent(baseFacts.Extent)
		}
	}
	startExpr, hasStart := optimizationCallArg(call, startIndex)
	endExpr, hasEnd := optimizationCallArg(call, endIndex)
	if !hasStart || !hasEnd {
		return nil
	}
	begin := optimizationExprString(startExpr)
	end := optimizationExprString(endExpr)
	if begin == "" || end == "" {
		return nil
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
	facts, ok := a.exprFacts[expr]
	return facts, ok
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
