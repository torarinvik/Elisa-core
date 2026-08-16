package semantic

import (
	"fmt"
	"path/filepath"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Optionals at the C ABI boundary.
//
// Elisa represents `T?` as a two-field struct {i1 tag, T payload}. C has no such type,
// so an optional in an `extern` signature has no C counterpart on its own. For payloads
// that lower to a pointer whose null value is not a legal payload, though, C already has
// the encoding Elisa wants: the pointer itself carries presence, with null meaning
// absent. That is exactly the C convention an opaque handle getter follows
// (`LLVMGetNamedFunction` returns NULL when the name is unknown).
//
// So `-> T?` on an extern is supported precisely when T has that null niche: the extern
// is declared to the linker as returning the bare payload pointer, and the optional is
// rebuilt at the call site from a null test. Optionals over any other payload (integers,
// structs, other optionals) have no such encoding and are rejected — see
// checkExternOptionalABI.
//
// WHICH externs are C boundaries. `extern` in Elisa means "declared here, defined
// elsewhere" — it does NOT by itself mean "native C". A module interface (.elisai)
// declares its Elisa functions with `extern` too, and those use Elisa's own ABI. The two
// are not distinguished at the surface, so this file treats only NON-GENERIC externs as
// possible C boundaries (FuncType.IsNativeExtern): C has no generics, so a generic extern
// is necessarily an Elisa interface declaration and is left entirely alone. Where an
// Elisa implementation for a non-generic extern is compiled alongside the declaration,
// the implementation's symbol replaces the extern's (defineExternImplementationGlobal),
// so its non-extern FuncType is what reaches lowering and nothing here applies.

// unitIsModuleInterface reports whether the unit under analysis is a MODULE INTERFACE
// (`.elisai`) rather than surface source.
//
// The distinction matters because `extern` is overloaded (see the note above): in a
// `.elisa` it usually declares a native C function, but every declaration in a `.elisai`
// is an Elisa function from another Elisa unit, using Elisa's own ABI. The old heuristic
// — "a generic extern is necessarily an interface declaration" — covers only the generic
// half, so a NON-GENERIC interface declaration was treated as a C boundary and put
// through the C-ABI checks.
//
// That made `-emit iface` produce a file the compiler then REFUSED to read: an Elisa
// function returning `i64?` is fine, its interface declaration says `extern f() -> i64?`,
// and re-reading that hit "return type of extern cannot be the optional type i64?".
// Measured over test/repro, 5 of 44 generated interfaces could not be re-consumed.
//
// The file extension settles it exactly, with no heuristic and no effect on `.elisa`
// sources.
func (a *Analyzer) unitIsModuleInterface() bool {
	if a == nil || a.file == nil {
		return false
	}
	return strings.EqualFold(filepath.Ext(a.file.Filename), ".elisai")
}

// NullNichePointerPayload reports whether t lowers to a single pointer for which null is
// not a valid value, and can therefore encode `t?` as a plain nullable C pointer.
//
// Kept in one place because the semantic check and the backend's boundary lowering must
// agree exactly: anything accepted here MUST be lowerable by the backend adapter, and
// anything rejected here must never reach it.
func NullNichePointerPayload(t Type) bool {
	switch tt := t.(type) {
	case *OpaqueType:
		// An `extern T` handle is a C pointer to an undescribed type; the C convention
		// is that a valid handle is never NULL.
		return true
	case *FuncType:
		// A function pointer; no valid function has address NULL.
		return true
	case *RefType:
		// A reference already lowers to a bare pointer. A NON-NULL ref is the niche
		// case: null is outside its value set, so it can mean "absent". A ref that is
		// itself nullable (`T&?`) already uses null for absence, so wrapping it in an
		// optional would need two distinct nulls — it has no niche left to give.
		return tt.State == RefStateNonNull
	default:
		return false
	}
}

// NicheOptionalPayload returns the payload of an optional that can cross a C ABI
// boundary as a plain nullable pointer, per NullNichePointerPayload.
func NicheOptionalPayload(t Type) (Type, bool) {
	optional, ok := t.(*OptionalType)
	if !ok || optional.Value == nil {
		return nil, false
	}
	if !NullNichePointerPayload(optional.Value) {
		return nil, false
	}
	return optional.Value, true
}

// checkExternOptionalABI rejects optionals in an extern signature that have no C
// encoding. Without this the backend would declare the native function with Elisa's
// {tag, payload} struct in place of a C type the callee never agreed to, and the call
// would silently read its result out of the wrong register.
//
// Gated on the same IsNativeExtern predicate the backend lowering uses, so the check and
// the lowering cannot disagree about which signatures are C boundaries.
//
// Only top-level parameter and return types are checked: an optional nested inside a
// by-value struct is a struct-layout question, governed by the repr(C) rules.
func (a *Analyzer) checkExternOptionalABI(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || !fnType.IsNativeExtern {
		return
	}
	for i, param := range fnType.Params {
		if i >= len(fn.Params) {
			break
		}
		a.checkExternOptionalABIType(param, fn.Params[i].Position, "parameter %q of extern %q", fn.Params[i].Name, fn.Name)
	}
	a.checkExternOptionalABIType(fnType.Return, fn.Position, "return type of extern %q", fn.Name)
}

func (a *Analyzer) checkExternOptionalABIType(t Type, pos lexer.Pos, context string, args ...interface{}) {
	optional, ok := t.(*OptionalType)
	if !ok || optional.Value == nil || NullNichePointerPayload(optional.Value) {
		return
	}
	where := fmt.Sprintf(context, args...)
	a.errorf(pos, "%s cannot be the optional type %s: C has no representation for an optional, "+
		"and %s has no null value to encode absence with. `T?` crosses a C boundary only when T is a "+
		"pointer whose null is spare — an `extern` handle type, a function, or a non-null reference. "+
		"Return the payload directly, or model absence the way the C function does (a sentinel value, "+
		"or an out-parameter)", where, optional.String(), optional.Value.String())
}
