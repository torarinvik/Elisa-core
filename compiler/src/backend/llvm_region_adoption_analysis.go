//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"reflect"
)

func (s *functionState) callerStorageArenasForBody(decl *ast.FuncDecl, fnType *semantic.FuncType, body []ast.Stmt) map[string]C.LLVMValueRef {
	result := map[string]C.LLVMValueRef{}
	if s == nil || decl == nil || fnType == nil {
		return result
	}
	paramArenas := map[string]C.LLVMValueRef{}
	explicitCount := backendExplicitParamCount(fnType, decl)
	if explicitCount > len(decl.Params) {
		explicitCount = len(decl.Params)
	}
	for i := 0; i < explicitCount; i++ {
		region := containerRegionName(fnType.Params[i])
		if region == "" {
			continue
		}
		if owner, ok := s.regionArenaOwner(region); ok && owner.arenaRef != nil {
			paramArenas[decl.Params[i].Name] = owner.arenaRef
		}
	}
	// Dependencies can only propagate an existing caller-owned arena. With no
	// seed arena, walking the body cannot add anything to the result.
	if len(paramArenas) == 0 {
		return result
	}
	dependencies := map[string]map[string]bool{}
	// ownerOrder lists dependencies' keys in source order (first mutating call). The fixed point
	// walks owners in this order so that a local copied into several caller aggregates
	// (`active.push(x)`, `seen.push(x)`) is deterministically given the FIRST one's arena. Ranging
	// over the map instead picked a different arena on different runs of the same compile.
	var ownerOrder []string
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && backendMutatesAggregateMethod(field.Field) {
					dst := backendAggregateRootName(field.Object)
					if dst != "" {
						deps := dependencies[dst]
						if deps == nil {
							deps = map[string]bool{}
							dependencies[dst] = deps
							ownerOrder = append(ownerOrder, dst)
						}
						for _, arg := range call.Args {
							for name := range backendAggregateIdentifierNames(arg) {
								deps[name] = true
							}
						}
					}
				}
			}
			walk(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for _, i := range astWalkFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	for name, arena := range paramArenas {
		result[name] = arena
	}
	for changed := true; changed; {
		changed = false
		for _, owner := range ownerOrder {
			sources := dependencies[owner]
			arena, escapes := result[owner]
			if !escapes || arena == nil {
				continue
			}
			for source := range sources {
				if _, already := result[source]; already {
					continue
				}
				result[source] = arena
				changed = true
			}
		}
	}
	return result
}

// adoptedEscapeNamesForBody computes the ownership fact needed when a synthesized auto region
// adopts a caller arena. A local returned from the function, copied as an ordinary call argument,
// or used to initialize another value that may escape must remain in the caller arena. Method
// receivers are deliberately excluded: `report.push(value)` mutates report in place, but its
// argument is the only value that can be copied into report. The scan is conservative; a false
// positive retains caller ownership, while a false negative would leave a dangling container
// header after the temporary arena is freed.
//
// roots are the function's parameters: a value copied into a parameter-rooted aggregate
// (`out.push(e)`, `p.items.push(v)`) is observed by the caller, so it escapes exactly like a
// returned value.
func adoptedEscapeNamesForBody(body []ast.Stmt, roots ...string) map[string]bool {
	escaped := map[string]bool{}
	for _, root := range roots {
		if root != "" {
			escaped[root] = true
		}
	}
	// dependencies records values copied into a mutable aggregate.  A returned
	// aggregate is an escape root, so every value copied into it must use the
	// caller-owned region too.  This is especially important for a struct pushed
	// into a returned darray: a shallow aggregate copy otherwise leaves a nested
	// darray backing in adopted.scratch, which is freed before the return value is
	// observed.
	dependencies := map[string]map[string]bool{}
	addDependencies := func(dst string, values []ast.Expr) {
		if dst == "" {
			return
		}
		deps := dependencies[dst]
		if deps == nil {
			deps = map[string]bool{}
			dependencies[dst] = deps
		}
		var collectNames func(reflect.Value)
		collectNames = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			if v.Kind() == reflect.Interface {
				if !v.IsNil() {
					collectNames(v.Elem())
				}
				return
			}
			if v.Kind() == reflect.Pointer {
				if v.IsNil() {
					return
				}
				if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
					deps[ident.Name] = true
					return
				}
				collectNames(v.Elem())
				return
			}
			switch v.Kind() {
			case reflect.Struct:
				for _, i := range astWalkFields(v.Type()) {
					collectNames(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				if !astTypeMayHoldNodes(v.Type().Elem()) {
					break
				}
				for i := 0; i < v.Len(); i++ {
					collectNames(v.Index(i))
				}
			}
		}
		for _, value := range values {
			collectNames(reflect.ValueOf(value))
		}
	}
	var aggregateRootName func(ast.Expr) string
	aggregateRootName = func(expr ast.Expr) string {
		switch n := expr.(type) {
		case *ast.Ident:
			if n != nil {
				return n.Name
			}
		case *ast.FieldExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.IndexExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.SliceExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.ParenExpr:
			if n != nil {
				return aggregateRootName(n.Inner)
			}
		case *ast.AddrOfExpr:
			if n != nil {
				return aggregateRootName(n.Operand)
			}
		}
		return ""
	}
	// These are the built-in mutators that copy their ordinary argument values
	// into a receiver.  Read-only methods are intentionally excluded so a value
	// returned from a function does not unnecessarily retain unrelated scratch
	// allocations.
	mutatesAggregate := func(name string) bool {
		switch name {
		case "push", "extend", "insert", "put", "add", "append", "set", "update":
			return true
		default:
			return false
		}
	}
	var collectExpr func(reflect.Value)
	collectExpr = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return
			}
			collectExpr(v.Elem())
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
				escaped[ident.Name] = true
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				// A field/scope callee is a receiver, not a copied value. Its
				// ordinary arguments are still escape candidates. A method that may
				// hand back a view of its receiver (`d.as_sview()`) makes the
				// receiver itself an escape candidate too.
				if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && (!adoptedEscapeReadOnlyMethods[field.Field] || adoptedEscapeElementMethods[field.Field]) {
					collectExpr(reflect.ValueOf(field.Object))
				}
				for _, arg := range call.Args {
					collectExpr(reflect.ValueOf(arg))
				}
				return
			}
			collectExpr(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for _, i := range astWalkFields(v.Type()) {
				collectExpr(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
			for i := 0; i < v.Len(); i++ {
				collectExpr(v.Index(i))
			}
		}
	}
	var walkStmt func(reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return
			}
			walkStmt(v.Elem())
			return
		}
		if v.Kind() != reflect.Pointer {
			switch v.Kind() {
			case reflect.Struct:
				for _, i := range astWalkFields(v.Type()) {
					walkStmt(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				if !astTypeMayHoldNodes(v.Type().Elem()) {
					break
				}
				for i := 0; i < v.Len(); i++ {
					walkStmt(v.Index(i))
				}
			}
			return
		}
		if v.IsNil() {
			return
		}
		if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
			field, isField := call.Func.(*ast.FieldExpr)
			if isField && field != nil && mutatesAggregate(field.Field) {
				addDependencies(aggregateRootName(field.Object), call.Args)
			} else if !isField || field == nil || !adoptedEscapeReadOnlyMethods[field.Field] {
				// Any other call may store one operand into another through a writable
				// parameter (`put(out, E{name: xs.as_sview()})`): each place operand depends
				// on the others, so an escaping owner (a caller-visible parameter) keeps
				// what the callee may store into it in the caller arena.
				operands := append([]ast.Expr(nil), call.Args...)
				if isField && field != nil {
					operands = append(operands, field.Object)
				}
				for i, operand := range operands {
					owner := aggregateRootName(operand)
					if owner == "" {
						continue
					}
					others := make([]ast.Expr, 0, len(operands)-1)
					for j, other := range operands {
						if j != i {
							others = append(others, other)
						}
					}
					addDependencies(owner, others)
				}
			}
		}
		if ret, ok := v.Interface().(*ast.ReturnStmt); ok && ret != nil {
			collectExpr(reflect.ValueOf(ret.Value))
		}
		if decl, ok := v.Interface().(*ast.VarDeclStmt); ok && decl != nil {
			// Propagate ownership through `local = other`; if the local is later
			// returned/copied, keeping the source in scratch would be unsound.
			collectExpr(reflect.ValueOf(decl.Value))
		}
		if assign, ok := v.Interface().(*ast.AssignStmt); ok && assign != nil {
			collectExpr(reflect.ValueOf(assign.Value))
		}
		walkStmt(v.Elem())
	}
	walkStmt(reflect.ValueOf(body))
	// Propagate escape through aggregate-copy edges until reaching a fixed point.
	// This handles chains such as `inner` -> `arms_buffer` -> return, while still
	// keeping unrelated scratch containers private.
	for changed := true; changed; {
		changed = false
		for owner, sources := range dependencies {
			if !escaped[owner] {
				continue
			}
			for source := range sources {
				if !escaped[source] {
					escaped[source] = true
					changed = true
				}
			}
		}
	}
	return escaped
}

func (s *functionState) ensureAdoptedScratchArena() error {
	if s == nil || s.scratchArena != nil {
		return nil
	}
	if s.g == nil || s.g.result == nil || s.g.namedType("Arena") == nil {
		return fmt.Errorf("missing builtin Arena type for adopted scratch arena")
	}
	arenaType := s.g.namedType("Arena")
	alloca, err := s.createEntryAllocaZeroed("adopted.scratch", arenaType)
	if err != nil {
		return err
	}
	s.scratchArena = alloca
	s.registerFunctionCleanup(scopedCleanupBinding{kind: scopedCleanupRegion, name: "adopted.scratch", ptr: alloca, typ: arenaType})
	return nil
}

// prepareAdoptedEscapeNames delays the reflective AST walk until a function
// actually enters a synthesized region that adopts its caller's arena. Most
// emitted functions never create an adopted scratch arena, so scanning their
// bodies cannot affect code generation.
func (s *functionState) prepareAdoptedEscapeNames() {
	if s == nil || s.adoptedEscapeReady {
		return
	}
	var roots []string
	if s.decl != nil {
		roots = paramNames(s.decl.Params)
	}
	s.adoptedEscapeNames = adoptedEscapeNamesForBody(s.adoptedEscapeBody, roots...)
	s.adoptedEscapeReady = true
}

func (s *functionState) adoptedScratchForLocal(name string) C.LLVMValueRef {
	if s == nil || s.scratchArena == nil {
		return nil
	}
	if s.adoptedEscapeNames != nil && s.adoptedEscapeNames[name] {
		return nil
	}
	return s.scratchArena
}
