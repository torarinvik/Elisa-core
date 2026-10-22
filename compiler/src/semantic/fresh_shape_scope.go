package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"strings"
)

type freshShapeIdentity struct {
	scope   string
	ordinal int
}

func (s *FreshShape) identity() freshShapeIdentity {
	return freshShapeIdentity{scope: s.Scope, ordinal: s.ID}
}

// Scope counters to the body, not its worker's previously analyzed declarations.
// The exact scope key also separates concrete generic instantiations and inference
// passes, whose expression types can coexist in the analyzer. Ordinals retain
// the existing per-typing-event freshness within each body; nested inference and
// lambda analysis cannot consume the enclosing body's allocation sequence.
func (a *Analyzer) enterFreshShapeScope(fn *ast.FuncDecl, args []Type, kind string) func() {
	var owner strings.Builder
	if fn != nil {
		name := fn.Name
		if sym, ok := a.symbolForFuncDecl(fn); ok && sym != nil {
			name = sym.Name
			// Hook linker symbols contain line numbers; their function type keeps
			// the original qualified source name.
			if ft, ok := sym.Type.(*FuncType); ok && ft.Name != "" {
				name = ft.Name
			}
		}
		p := fn.Pos()
		// Positions shift after unrelated retained edits. Source signatures keep
		// overloads distinct without depending on worker order or line offsets.
		fmt.Fprintf(&owner, "%q:%q:%q:%t", p.File, name, kind, fn.Variadic)
		for _, generic := range fn.GenericParams {
			fmt.Fprintf(&owner, "|generic:%d:%q:%q:%q:%q", generic.Kind, generic.Name, generic.InterfaceBound, generic.StateCases, generic.StateOwner)
		}
		for _, typeParam := range fn.TypeParams {
			fmt.Fprintf(&owner, "|type-param:%q", typeParam)
		}
		for _, regionParam := range fn.RegionParams {
			fmt.Fprintf(&owner, "|region-param:%q", regionParam)
		}
		for _, permissionParam := range fn.PermissionParams {
			fmt.Fprintf(&owner, "|permission-param:%q", permissionParam)
		}
		for _, param := range fn.Params {
			fmt.Fprintf(&owner, "|param:%q:%t:%q", param.Name, param.Mutable, unparse.FormatType(param.Type))
		}
		fmt.Fprintf(&owner, "|return:%q:%q", unparse.FormatType(fn.ReturnType), fn.ReturnRegion)
	} else {
		fmt.Fprintf(&owner, "anonymous:%q", kind)
	}
	for _, arg := range args {
		key := freshShapeScopeTypeKey(arg)
		fmt.Fprintf(&owner, "|%d:%s", len(key), key)
	}
	return a.useFreshShapeScope(owner.String())
}

func (a *Analyzer) enterLambdaFreshShapeScope(expr *ast.LambdaExpr, params []Type) func() {
	a.freshShapeLambdaCounter++
	p := expr.Pos()
	var owner strings.Builder
	fmt.Fprintf(&owner, "%d:%s|lambda:%q:%d", len(a.freshShapeScope), a.freshShapeScope, p.File, a.freshShapeLambdaCounter)
	for _, param := range params {
		key := freshShapeScopeTypeKey(param)
		fmt.Fprintf(&owner, "|%d:%s", len(key), key)
	}
	return a.useFreshShapeScope(owner.String())
}

// The canonical encoding retains exact fresh identities and regions. The symbol
// encoding additionally retains complete constant-record arguments; neither
// process-assigned TypeIDs nor map iteration order participates in this key.
func freshShapeScopeTypeKey(t Type) string {
	canonical, ok := canonicalTypeIDKey(t)
	if !ok {
		canonical = fmt.Sprintf("%T:%s", t, t)
	}
	symbol, err := TypeSymbolKey(t)
	if err != nil {
		return canonical
	}
	return fmt.Sprintf("%d:%s|%s", len(canonical), canonical, symbol)
}

func (a *Analyzer) useFreshShapeScope(scope string) func() {
	savedScope, savedCounter, savedLambdas := a.freshShapeScope, a.freshShapeCounter, a.freshShapeLambdaCounter
	a.freshShapeScope, a.freshShapeCounter, a.freshShapeLambdaCounter = scope, 0, 0
	return func() {
		a.freshShapeScope, a.freshShapeCounter, a.freshShapeLambdaCounter = savedScope, savedCounter, savedLambdas
	}
}
