package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

// EffectHandler is the compile-time description of a static realization of an
// abstract effect. Its operation methods are ordinary hidden functions. The
// handler itself is never represented as a runtime value.
type EffectHandler struct {
	Name          string
	EffectName    string
	Decl          *ast.ImplDecl
	MethodSymbols map[string]string
	ConcreteRefs  []ast.PermissionRef
	Static        bool
}

// EffectHandlerMethodSymbolName is deliberately stable and independent of the
// handler's captured resource types. A handler operation is specialized by its
// call-site arguments, just like any other Elisa function.
func EffectHandlerMethodSymbolName(handlerName, methodName string) string {
	return "__handler__" + sanitizeStaticInterfaceSymbolFragment(handlerName) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

// nextHandlerCaptureName is frontend-only. A handler installation is lowered
// to ordinary local bindings so each capture expression is evaluated once;
// the generated name never becomes a runtime handler object or dispatch key.
func (a *Analyzer) nextHandlerCaptureName() string {
	name := fmt.Sprintf("__handler_capture_%d", a.handlerCaptureCounter)
	a.handlerCaptureCounter++
	return name
}

func effectHeadName(typ ast.TypeExpr) string {
	switch n := typ.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.GenericType:
		return n.Name
	case *ast.MutableType:
		return effectHeadName(n.Elem)
	case *ast.RefType:
		return effectHeadName(n.Elem)
	default:
		return ""
	}
}

func (a *Analyzer) collectEffectPermissions(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.InterfaceDecl)
		if !ok || decl == nil || !decl.IsEffect {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		if existing, exists := a.permissions[qualifiedName]; exists {
			if existing.Abstract {
				continue
			}
			a.errorf(decl.Pos(), "abstract effect %q conflicts with permission family %q", qualifiedName, qualifiedName)
			continue
		}
		members := make([]string, 0, len(decl.Members))
		memberSet := make(map[string]bool, len(decl.Members))
		for _, member := range decl.Members {
			var name string
			switch n := member.(type) {
			case *ast.ExternFuncDecl:
				name = n.Name
			case *ast.FuncDecl:
				name = n.Name
			}
			if name == "" {
				continue
			}
			if memberSet[name] {
				a.errorf(member.Pos(), "duplicate abstract effect operation %q in %q", name, qualifiedName)
				continue
			}
			memberSet[name] = true
			members = append(members, name)
		}
		a.permissions[qualifiedName] = &PermissionSet{
			Name:      qualifiedName,
			Members:   members,
			MemberSet: memberSet,
			Abstract:  true,
			Effect:    decl,
		}
	}
}

func (a *Analyzer) collectEffectHandlers(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ImplDecl)
		if !ok || decl == nil || !decl.IsHandler {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			name := joinQualifiedName(scoped.Namespace, decl.HandlerName)
			if _, exists := a.effectHandlers[name]; exists {
				a.errorf(decl.Pos(), "duplicate effect handler %q", name)
				return
			}
			effectName := effectHeadName(decl.HandlerEffect)
			effect, qualifiedEffectName, exists := a.lookupVisibleStaticInterface(effectName)
			if !exists || effect == nil || effect.Decl == nil || !effect.Decl.IsEffect {
				a.errorf(decl.Pos(), "handler %q targets %q, which is not an abstract effect", name, effectName)
				return
			}
			if effectName == "" {
				a.errorf(decl.Pos(), "effect handler %q is missing its target effect", name)
				return
			}
			handler := &EffectHandler{
				Name:          name,
				EffectName:    qualifiedEffectName,
				Decl:          decl,
				MethodSymbols: map[string]string{},
				Static:        true,
			}
			a.validateAndLowerHandlerResumptions(handler)
			seen := map[string]bool{}
			for _, member := range decl.Members {
				var methodName string
				switch n := member.(type) {
				case *ast.FuncDecl:
					methodName = n.Name
				case *ast.ExternFuncDecl:
					methodName = n.Name
				}
				if methodName == "" {
					continue
				}
				if seen[methodName] {
					a.errorf(member.Pos(), "duplicate handler operation %q in %q", methodName, name)
					continue
				}
				seen[methodName] = true
				if !effect.Methods[methodName].SignatureValid() {
					a.errorf(member.Pos(), "effect %q has no operation %q", qualifiedEffectName, methodName)
					continue
				}
				handler.MethodSymbols[methodName] = EffectHandlerMethodSymbolName(name, methodName)
			}
			a.effectHandlers[name] = handler
		})
	}
}

// handlerReturnIsVoid is deliberately conservative. A missing return type is
// the normal void spelling for a handler method; every explicit non-void type
// is rejected for tail resumption because returning a continuation result would
// require a runtime protocol that is outside the zero-overhead subset.
func handlerReturnIsVoid(typ ast.TypeExpr) bool {
	if typ == nil {
		return true
	}
	switch n := typ.(type) {
	case *ast.NamedType:
		return n.Name == "void"
	case *ast.GenericType:
		return n.Name == "void"
	default:
		return false
	}
}

func isResumeCallExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) != 0 {
		return false
	}
	return isResumeCallName(call)
}

func isResumeCallName(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	ident, ok := call.Func.(*ast.Ident)
	return ok && ident != nil && ident.Name == "resume"
}

func isResumeStmt(stmt ast.Stmt) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	return ok && exprStmt != nil && isResumeCallExpr(exprStmt.Expr)
}

// countResumeCalls is used only while validating handler declarations. It is
// intentionally a frontend-only walk; the generated program never contains a
// continuation object or this marker call.
func countResumeCalls(value reflect.Value) int {
	if !value.IsValid() {
		return 0
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return 0
		}
		return countResumeCalls(value.Elem())
	}
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return 0
		}
		if value.CanInterface() {
			if call, ok := value.Interface().(*ast.CallExpr); ok && isResumeCallName(call) {
				count := 1
				for _, arg := range call.Args {
					count += countResumeCalls(reflect.ValueOf(arg))
				}
				return count
			}
		}
		return countResumeCalls(value.Elem())
	}
	total := 0
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			total += countResumeCalls(value.Index(i))
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.CanInterface() {
				total += countResumeCalls(field)
			}
		}
	}
	return total
}

// validateAndLowerHandlerResumptions implements the entire resumable portion
// of static handlers. The only supported shape is a single final `resume()` in
// a void operation. Since the caller continues immediately after the direct
// handler call, that marker lowers to an ordinary return and has zero runtime
// overhead. Anything that could require saving, copying, or dynamically
// invoking a continuation is rejected here.
func (a *Analyzer) validateAndLowerHandlerResumptions(handler *EffectHandler) {
	if handler == nil || handler.Decl == nil {
		return
	}
	for _, member := range handler.Decl.Members {
		fn, ok := member.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		resumeCount := countResumeCalls(reflect.ValueOf(fn.Body))
		if resumeCount == 0 {
			continue
		}
		tail := len(fn.Body) != 0 && isResumeStmt(fn.Body[len(fn.Body)-1])
		if resumeCount != 1 || !tail {
			a.errorf(fn.Pos(), "zero-overhead handler resume in %q must be exactly one final resume() statement; resumability is inferred from this shape", fn.Name)
		}
		if !handlerReturnIsVoid(fn.ReturnType) {
			a.errorf(fn.Pos(), "zero-overhead handler resume in %q requires a void operation", fn.Name)
		}
		// Erase the marker even after a mode error when it is in the one
		// syntactic position that has unambiguous tail semantics. This avoids
		// cascading undefined-name errors while preserving the real diagnostic.
		if tail {
			fn.Body[len(fn.Body)-1] = &ast.ReturnStmt{Position: fn.Body[len(fn.Body)-1].Pos()}
		}
	}
}

// SignatureValid keeps handler collection tolerant of a malformed effect
// member while still avoiding a nil dereference in the normal path.
func (m *StaticInterfaceMethod) SignatureValid() bool {
	return m != nil && m.Signature != nil
}

func (a *Analyzer) lookupVisibleEffectHandler(name string) (*EffectHandler, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if handler, ok := a.effectHandlers[candidate]; ok {
			return handler, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) handlerForCan(stmt *ast.CanStmt, refs []ast.PermissionRef) *EffectHandler {
	if stmt == nil || stmt.HandlerName == "" {
		return nil
	}
	handler, _, ok := a.lookupVisibleEffectHandler(stmt.HandlerName)
	if !ok || handler == nil {
		a.errorf(stmt.Position, "unknown effect handler %q", stmt.HandlerName)
		return nil
	}
	found := false
	for _, ref := range refs {
		if a.permissionRefMatchesHandler(ref, handler) {
			found = true
			break
		}
	}
	if !found {
		a.errorf(stmt.Position, "handler %q realizes %q, but the can block does not grant that exact abstract effect specialization", handler.Name, unparse.FormatType(handler.Decl.HandlerEffect))
		return nil
	}
	return handler
}

// permissionRefMatchesHandler requires an exact abstract-effect specialization.
// Matching only the family would let a handler for Writer[sview] silently handle
// Writer[i64], which is both unsound and incompatible with direct-call lowering.
func (a *Analyzer) permissionRefMatchesHandler(ref ast.PermissionRef, handler *EffectHandler) bool {
	if handler == nil || handler.Decl == nil || ref.Member != "" {
		return false
	}
	qualified := ref.Name
	if _, candidate, ok := a.lookupVisiblePermission(ref.Name); ok {
		qualified = candidate
	}
	if qualified != handler.EffectName {
		return false
	}
	target, ok := handler.Decl.HandlerEffect.(*ast.GenericType)
	if !ok {
		return len(ref.TypeArgs) == 0
	}
	if len(target.Args) != len(ref.TypeArgs) {
		return false
	}
	for index, argument := range target.Args {
		if unparse.FormatType(argument) != unparse.FormatType(ref.TypeArgs[index]) {
			return false
		}
	}
	return true
}

func handlerConcretePermissionRefs(handler *EffectHandler) []ast.PermissionRef {
	if handler == nil {
		return nil
	}
	return append([]ast.PermissionRef(nil), handler.ConcreteRefs...)
}

// addHandlerConcretePermissionRefs mirrors handlerForCan without emitting a
// second diagnostic. The flow pass validates the installation; the permission
// usage pass runs independently and needs the handler's concrete authority in
// its lexical grant set as well.
func (a *Analyzer) addHandlerConcretePermissionRefs(stmt *ast.CanStmt, refs []ast.PermissionRef) []ast.PermissionRef {
	if stmt == nil || stmt.HandlerName == "" {
		return refs
	}
	handler, _, ok := a.lookupVisibleEffectHandler(stmt.HandlerName)
	if !ok || handler == nil {
		return refs
	}
	for _, ref := range refs {
		if a.permissionRefMatchesHandler(ref, handler) {
			return mergePermissionRefs(refs, handlerConcretePermissionRefs(handler))
		}
	}
	return refs
}

type effectRewriteContext struct {
	handler *EffectHandler
	args    []ast.Expr
	parent  *effectRewriteContext
}

// rewriteEffectHandlers performs the zero-runtime-cost part of handler
// installation. It walks the already parsed function body, switches context at
// `can Effect with Handler(args):`, and replaces `Effect.operation(...)` with a
// direct call to the handler's hidden operation function.
//
// Reflection is used only for this AST normalization pass. It keeps the walker
// complete as new statement/expression nodes are added to the frontend; no
// reflection survives into semantic analysis or generated code.
func (a *Analyzer) rewriteEffectHandlers(body []ast.Stmt) {
	if len(body) == 0 {
		return
	}
	a.rewriteEffectValue(reflect.ValueOf(body), effectRewriteContext{})
}

func (a *Analyzer) rewriteEffectValue(value reflect.Value, context effectRewriteContext) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		a.rewriteEffectValue(value.Elem(), context)
		return
	}
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return
		}
		if value.CanInterface() {
			switch node := value.Interface().(type) {
			case *ast.CanStmt:
				// Handler arguments are evaluated in the enclosing scope. Normalize
				// them before entering the new handler context, then bind each one
				// once at the start of the block. Reusing the original expression in
				// every direct operation call would duplicate side effects and could
				// duplicate moves/borrows.
				a.rewriteEffectValue(reflect.ValueOf(node.HandlerArgs), context)
				next := context
				captureDecls := make([]ast.Stmt, 0, len(node.HandlerArgs))
				captureArgs := make([]ast.Expr, 0, len(node.HandlerArgs))
				for _, argument := range node.HandlerArgs {
					name := a.nextHandlerCaptureName()
					captureDecls = append(captureDecls, &ast.VarDeclStmt{
						Position: node.Position,
						Name:     name,
						Value:    argument,
					})
					captureArgs = append(captureArgs, &ast.Ident{Position: node.Position, Name: name})
				}
				node.HandlerArgs = captureArgs
				if node.HandlerName != "" {
					if handler, _, ok := a.lookupVisibleEffectHandler(node.HandlerName); ok {
						// Keep the enclosing context so an inner partial handler can
						// forward an operation to the next installed realization at
						// compile time. This pointer never reaches generated code.
						next = effectRewriteContext{handler: handler, args: append([]ast.Expr(nil), captureArgs...), parent: &context}
					}
				}
				body := node.Body
				a.rewriteEffectValue(reflect.ValueOf(body), next)
				if len(captureDecls) != 0 {
					node.Body = append(captureDecls, body...)
				}
				return
			case *ast.CallExpr:
				a.rewriteEffectCall(node, context)
			}
		}
		a.rewriteEffectValue(value.Elem(), context)
		return
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			a.rewriteEffectValue(value.Index(i), context)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.CanInterface() {
				a.rewriteEffectValue(field, context)
			}
		}
	}
}

func (a *Analyzer) rewriteEffectCall(call *ast.CallExpr, context effectRewriteContext) {
	if call == nil {
		return
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil {
		return
	}
	ownerName, ok := qualifiedTypePathFromExpr(field.Object)
	if !ok || ownerName == "" {
		return
	}
	effect, qualifiedOwner, isEffect := a.lookupVisibleStaticInterface(ownerName)
	if !isEffect || effect == nil || effect.Decl == nil || !effect.Decl.IsEffect {
		return
	}
	if _, ok := effect.Methods[field.Field]; !ok {
		return
	}
	var selected *effectRewriteContext
	var matching *effectRewriteContext
	for current := &context; current != nil; current = current.parent {
		if current.handler == nil || current.handler.EffectName != qualifiedOwner {
			continue
		}
		if matching == nil {
			matching = current
		}
		if current.handler.MethodSymbols[field.Field] != "" {
			selected = current
			break
		}
	}
	if selected == nil && matching == nil {
		a.errorf(call.Pos(), "abstract effect operation %s.%s requires an installed handler", ownerName, field.Field)
		return
	}
	if selected == nil {
		a.errorf(call.Pos(), "handler %q does not implement operation %q of effect %q, and no enclosing handler forwards it", matching.handler.Name, field.Field, matching.handler.EffectName)
		return
	}
	symbolName := selected.handler.MethodSymbols[field.Field]
	if symbolName == "" {
		a.errorf(call.Pos(), "handler %q does not implement operation %q of effect %q", selected.handler.Name, field.Field, selected.handler.EffectName)
		return
	}
	originalArgs := append([]ast.Expr(nil), call.Args...)
	call.Args = make([]ast.Expr, 0, len(selected.args)+len(originalArgs))
	call.Args = append(call.Args, selected.args...)
	call.Args = append(call.Args, originalArgs...)
	call.Func = &ast.Ident{Position: field.Position, Name: symbolName}
	// The handler capture arguments are positional and the rewrite invalidates
	// every parser/analyzer argument-order cache attached to the old callee.
	call.ArgNames = nil
	call.ArgShorthand = nil
	call.ArgItemOrder = nil
	call.ResolvedArgsValid = false
	call.ResolvedImplicitArgsValid = false
}
