package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/lexer"
)

// docs/107 increment 5a — front-end wiring of the typed guest-memory overlay.
//
// This is the load-bearing front-end step the docs/107 §(f) increment 3 calls out: parse the
// overlay accessor surface form over a `GuestVAddr[L]` carrier, resolve the named field against the
// declared layout L, and lower it to the EXACT MemoryManager_ReadU<N>/WriteU<N> call the programmer
// writes by hand today. There is no codegen change on the success path (docs/107 §(e)
// ABI-neutrality): the desugar produces an ordinary CallExpr that the normal call path analyzes and
// the backend emits identically to the hand-written read/write.
//
// Surface syntax (docs/107 §(a), §(d)):
//
//	proc_param.mem_param[memory]              # read  -> MemoryManager_ReadU64(memory, proc_param + 64)
//	proc_param.size[memory] = 0               # write -> MemoryManager_WriteU64(memory, proc_param + 0, 0)
//
// `proc_param` is a `GuestVAddr[OrbisProcParam]` carrier, `mem_param`/`size` are declared fields of
// the registered layout `OrbisProcParam`, and `memory` is the MemoryManager to read/write through.
// The access width is the field's declared width (u64 -> ReadU64). Resolution and bounds/width
// checks resolve against the SAME `easm.Layout` description the EASM-side `N(%base)` reads use
// (docs/104), so the boot-assembly reads and these high-level reads cannot drift. The check logic
// (resolveOverlayField) mirrors easm's CheckGuestOverlayAccessSized; it is kept here so this
// front-end wiring does not depend on the parallel easm-prototype file landing first.
//
// The accessor is recognized only when the carrier's layout name is a registered overlay layout, so
// every existing field-access / index-access shape is untouched when no overlay layouts are
// supplied (AnalyzeOptions.OverlayLayouts empty).

// overlayLayoutRegistry indexes the supplied overlay layouts by name. Nil/empty input yields a nil
// map, and a nil map lookup is a clean miss, so the overlay path is fully inert by default.
func overlayLayoutRegistry(layouts []*easm.Layout) map[string]*easm.Layout {
	if len(layouts) == 0 {
		return nil
	}
	reg := make(map[string]*easm.Layout, len(layouts))
	for _, l := range layouts {
		if l != nil && l.Name != "" {
			reg[l.Name] = l
		}
	}
	return reg
}

// bareNamedTypeName returns the bare identifier of a type expression that is a simple name
// (`OrbisProcParam`), or "" for anything richer. Used to read a carrier's overlay layout name out
// of `GuestVAddr[Name]` without requiring Name to be a declared Elisa type.
func bareNamedTypeName(t ast.TypeExpr) string {
	if n, ok := t.(*ast.NamedType); ok {
		return n.Name
	}
	return ""
}

// overlayLayoutSize returns a layout's byte size: max(field.Offset + field.Width) over declared
// fields. This mirrors easm.LayoutSize for the size-less layouts of this branch (docs/107
// increment 2's explicit `size N` is a separate, parallel piece of work); it is sound for the
// offset/bounds checks below. A field with unknown width (Width == 0) contributes only its offset.
func overlayLayoutSize(l *easm.Layout) int64 {
	var max int64
	for _, f := range l.Fields {
		end := f.Offset + int64(f.Width)
		if end > max {
			max = end
		}
	}
	return max
}

// resolveOverlayField resolves a named field against a layout, applying the docs/107 §(b) static
// checks in order and returning (offset, width) on success or a (code, message) diagnostic on
// failure. This is the AST-side counterpart of easm.CheckGuestOverlayAccessSized — the same three
// checks (unknown-field / out-of-bounds / width-mismatch), kept here so the front-end wiring does
// not depend on the parallel easm-prototype file landing first.
func resolveOverlayField(l *easm.Layout, fieldName string) (offset int64, width int, code string, message string) {
	var field *easm.LayoutField
	for i := range l.Fields {
		if l.Fields[i].Name == fieldName {
			field = &l.Fields[i]
			break
		}
	}
	if field == nil {
		return 0, 0, "overlay-unknown-field", "field " + fieldName + " is not declared in layout " + l.Name + "; add the field or correct the access"
	}
	size := overlayLayoutSize(l)
	if field.Width != 0 && field.Offset+int64(field.Width) > size {
		return 0, 0, "overlay-field-out-of-bounds", "field " + l.Name + "." + field.Name + " ends past layout " + l.Name + " (" + intToDecimal(size) + " bytes); the read would run past the struct"
	}
	return field.Offset, field.Width, "", ""
}

// overlayReadFns / overlayWriteFns map a field byte width to the MemoryManager accessor the desugar
// lowers to. Mirrors easm.widthToReadFn/widthToWriteFn (which are unexported in the easm package).
var overlayReadFns = map[int]string{1: "MemoryManager_ReadU8", 2: "MemoryManager_ReadU16", 4: "MemoryManager_ReadU32", 8: "MemoryManager_ReadU64"}
var overlayWriteFns = map[int]string{1: "MemoryManager_WriteU8", 2: "MemoryManager_WriteU16", 4: "MemoryManager_WriteU32", 8: "MemoryManager_WriteU64"}

// guestOverlayFieldType reports, for a bare `carrier.field` whose carrier is a registered
// `GuestVAddr[L]` overlay carrier, the element type the field reads as (by declared width). It is
// used to resolve the overlay FieldExpr without a spurious struct-field error; the IndexExpr hook
// performs the actual lowering. ok is false for any non-overlay receiver or unknown field.
func (a *Analyzer) guestOverlayFieldType(objType Type, fieldName string) (Type, bool) {
	if a == nil || len(a.overlayLayouts) == 0 || fieldName == "" {
		return nil, false
	}
	addr, isAddr := objType.(*AddressSpaceType)
	if !isAddr || addr == nil || addr.Space != "guest" || addr.LayoutName == "" {
		return nil, false
	}
	layout := a.overlayLayouts[addr.LayoutName]
	if layout == nil {
		return nil, false
	}
	width := 0
	for i := range layout.Fields {
		if layout.Fields[i].Name == fieldName {
			width = layout.Fields[i].Width
			break
		}
	}
	switch width {
	case 1:
		return a.namedTypes["u8"], true
	case 2:
		return a.namedTypes["u16"], true
	case 4:
		return a.namedTypes["u32"], true
	case 8:
		return a.namedTypes["u64"], true
	}
	return nil, false
}

// overlayAccessorParts recognizes the read form `base.field[mem]` of an IndexExpr: an IndexExpr
// whose Object is a FieldExpr over a carrier identifier and whose Index is a bare identifier. It
// returns the field-access FieldExpr, the carrier base expression, the field name, and the mem
// identifier. ok is false for anything that is not this exact shape.
func overlayAccessorParts(expr *ast.IndexExpr) (base ast.Expr, field string, mem string, ok bool) {
	if expr == nil || expr.Fallback != nil {
		return nil, "", "", false
	}
	fieldExpr, isField := expr.Object.(*ast.FieldExpr)
	if !isField || fieldExpr == nil || fieldExpr.Field == "" || fieldExpr.Safe {
		return nil, "", "", false
	}
	memIdent, isIdent := expr.Index.(*ast.Ident)
	if !isIdent || memIdent == nil || memIdent.Name == "" {
		return nil, "", "", false
	}
	return fieldExpr.Object, fieldExpr.Field, memIdent.Name, true
}

// tryDesugarGuestOverlayIndex recognizes and rewrites a docs/107 overlay read accessor in place.
// When `expr` is `base.field[mem]` over a `GuestVAddr[L]`-with-registered-layout carrier, it
// resolves the field against L (resolveOverlayField: unknown-field / out-of-bounds / width-mismatch
// diagnostics, the AST-side mirror of easm's checker) and rewrites expr.AsOverlayCall to the equivalent
// MemoryManager_ReadU<N>(mem, base + offset) CallExpr, returning that call's analyzed type. The
// returned handled=false means "not an overlay accessor" and the caller proceeds normally.
func (a *Analyzer) tryDesugarGuestOverlayIndex(expr *ast.IndexExpr) (Type, bool) {
	if a == nil || len(a.overlayLayouts) == 0 {
		return nil, false
	}
	base, field, mem, ok := overlayAccessorParts(expr)
	if !ok {
		return nil, false
	}
	// The carrier must be a guest-address carrier whose layout name is registered.
	baseType := a.analyzeExpr(base)
	addr, isAddr := baseType.(*AddressSpaceType)
	if !isAddr || addr == nil || addr.Space != "guest" || addr.LayoutName == "" {
		return nil, false
	}
	layout := a.overlayLayouts[addr.LayoutName]
	if layout == nil {
		return nil, false
	}

	// Resolve the named field against the layout (unknown-field / out-of-bounds checks).
	offset, width, code, message := resolveOverlayField(layout, field)
	if code != "" {
		a.errorf(expr.Pos(), "%s: %s", code, message)
		return invalidType, true
	}
	// The access width is the field's declared width; a width with no ReadU<N> form (not 1/2/4/8)
	// is an overlay-field-width-mismatch — there is no accessor to lower to.
	readFn, hasFn := overlayReadFns[width]
	if !hasFn {
		a.errorf(expr.Pos(), "overlay-field-width-mismatch: field %s.%s has %d-byte width, which has no MemoryManager_ReadU<N> form (expected 1/2/4/8)", layout.Name, field, width)
		return invalidType, true
	}
	a.checkOverlaySizeGuard(expr.Pos(), layout, field, overlayCarrierKey(base))

	call := a.buildOverlayReadCall(expr.Position, readFn, mem, base, offset)
	expr.AsOverlayCall = call
	return a.analyzeExpr(call), true
}

// overlaySizeFieldName is the conventional name of a layout's runtime size field — the field a
// `requires size >= N` obligation refers to, and the field a dominating guard `if base.size[mem] >= N:`
// reads. docs/107 §(b) and the easm checker's diagnostic both spell it `size`.
const overlaySizeFieldName = "size"

// overlaySizeGuardFact is one proven lower-bound on a carrier's runtime size field, established by a
// dominating guard. carrier is the carrier key (see overlayCarrierKey); layout is its layout name.
type overlaySizeGuardFact struct {
	carrier string
	layout  string
	atLeast int64
}

// overlayCarrierKey returns a stable key identifying the carrier a `base.field[mem]` access (or a
// `base.size[mem]` guard) is taken through, so a guard and a later access on the SAME carrier match.
// Only a bare identifier carrier yields a key; richer base expressions return "" (no key), which
// never matches — sound, just conservative (the access stays unguarded and is rejected).
func overlayCarrierKey(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok && id != nil {
		return id.Name
	}
	return ""
}

// sizeGuardFactForCondition recognizes a carrier size comparison `base.size[mem] <op> N` and returns
// the proven size lower-bound that holds when the condition has truthiness `truthy`. The two callers
// are the dominator→SizeGuardFact derivation (docs/107 increment 4): the truthy branch of a positive
// guard (`if base.size[mem] >= N:` → fact holds in the body) and the fall-through after an
// early-return negation guard (`if base.size[mem] < N: return` → the NEGATION `size >= N` holds for
// the rest of the block). ok is false when the condition is not a carrier size guard or yields only
// an (unhelpful) upper bound at the requested truthiness.
func (a *Analyzer) sizeGuardFactForCondition(cond ast.Expr, truthy bool) (overlaySizeGuardFact, bool) {
	if a == nil || len(a.overlayLayouts) == 0 {
		return overlaySizeGuardFact{}, false
	}
	for {
		paren, ok := cond.(*ast.ParenExpr)
		if !ok {
			break
		}
		cond = paren.Inner
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin == nil {
		return overlaySizeGuardFact{}, false
	}
	op := bin.Op
	access, isIdx := bin.Left.(*ast.IndexExpr)
	bound := bin.Right
	if !isIdx {
		// Allow the flipped spelling `N <= base.size[mem]` / `N < ...`.
		access, isIdx = bin.Right.(*ast.IndexExpr)
		bound = bin.Left
		op = flipComparison(op)
	}
	if !isIdx {
		return overlaySizeGuardFact{}, false
	}
	base, field, _, ok := overlayAccessorParts(access)
	if !ok || field != overlaySizeFieldName {
		return overlaySizeGuardFact{}, false
	}
	carrier := overlayCarrierKey(base)
	if carrier == "" {
		return overlaySizeGuardFact{}, false
	}
	addr, isAddr := a.analyzeExpr(base).(*AddressSpaceType)
	if !isAddr || addr == nil || addr.Space != "guest" || addr.LayoutName == "" {
		return overlaySizeGuardFact{}, false
	}
	if a.overlayLayouts[addr.LayoutName] == nil {
		return overlaySizeGuardFact{}, false
	}
	c, ok := a.constIntValue(bound)
	if !ok {
		return overlaySizeGuardFact{}, false
	}
	// The fact is read off the condition under the requested truthiness: a false `size < N` proves
	// `size >= N`, a false `size <= N` proves `size > N` (i.e. `>= N+1`).
	if !truthy {
		op = negateComparison(op)
	}
	var atLeast int64
	switch op {
	case lexer.TOKEN_GTEQ:
		atLeast = c
	case lexer.TOKEN_GT:
		atLeast = c + 1
	default:
		return overlaySizeGuardFact{}, false
	}
	return overlaySizeGuardFact{carrier: carrier, layout: addr.LayoutName, atLeast: atLeast}, true
}

// applyOverlaySizeGuardForCondition pushes a size-guard fact for the truthy branch of a positive
// guard `if base.size[mem] >= N:` (or `> N`). The fact is active for the branch body; the caller
// truncates the stack when the branch completes.
func (a *Analyzer) applyOverlaySizeGuardForCondition(cond ast.Expr) {
	if fact, ok := a.sizeGuardFactForCondition(cond, true); ok {
		a.overlaySizeGuards = append(a.overlaySizeGuards, fact)
	}
}

// applyOverlayFallthroughGuard handles the early-return guard-clause idiom: an
// `if base.size[mem] < N: <exits>` statement with no elif/else whose then-branch definitely exits
// means control only continues when the condition is FALSE, so the negation `size >= N` dominates the
// rest of the enclosing block. The fact is pushed for the remaining statements; analyzeBlockInScope
// truncates it at the block boundary.
func (a *Analyzer) applyOverlayFallthroughGuard(stmt ast.Stmt) {
	if a == nil || len(a.overlayLayouts) == 0 {
		return
	}
	ifStmt, ok := stmt.(*ast.IfStmt)
	if !ok || len(ifStmt.Elifs) != 0 || len(ifStmt.Else) != 0 || !blockDefinitelyExits(ifStmt.Then) {
		return
	}
	if fact, ok := a.sizeGuardFactForCondition(ifStmt.Cond, false); ok {
		a.overlaySizeGuards = append(a.overlaySizeGuards, fact)
	}
}

// checkOverlaySizeGuard discharges a field's `requires size >= N` obligation (docs/107 increment 4)
// against the size-guard facts in scope. It collects the active facts for this carrier+layout and
// hands them to the easm checker; an undischarged obligation surfaces as overlay-field-needs-size-guard.
func (a *Analyzer) checkOverlaySizeGuard(pos lexer.Pos, layout *easm.Layout, fieldName, carrier string) {
	var facts []easm.SizeGuardFact
	for _, g := range a.overlaySizeGuards {
		if g.layout == layout.Name && g.carrier == carrier && carrier != "" {
			facts = append(facts, easm.SizeGuardFact{AtLeast: g.atLeast})
		}
	}
	for _, issue := range easm.CheckGuestOverlaySizeGuard("", pos.Line, layout, fieldName, facts) {
		a.errorf(pos, "%s: %s", issue.Code, issue.Message)
	}
}

// tryDesugarGuestOverlayWrite recognizes and rewrites the docs/107 overlay WRITE accessor
// `base.field[mem] = value` in place. When the assignment target is `base.field[mem]` over a
// `GuestVAddr[L]`-with-registered-layout carrier, it resolves the field against L (the same
// unknown-field / out-of-bounds / width-mismatch checks as the read form) and stashes the equivalent
// MemoryManager_WriteU<N>(mem, base + offset, value) CallExpr on stmt.AsOverlayCall, which the backend
// emits in place of the store. handled=false means "not an overlay write" and the caller proceeds with
// the normal assignment path. On a resolution error it reports and returns handled=true (consumed).
func (a *Analyzer) tryDesugarGuestOverlayWrite(stmt *ast.AssignStmt) bool {
	if a == nil || len(a.overlayLayouts) == 0 || stmt == nil || stmt.Optional {
		return false
	}
	target, ok := stmt.Target.(*ast.IndexExpr)
	if !ok {
		return false
	}
	base, field, mem, ok := overlayAccessorParts(target)
	if !ok {
		return false
	}
	a.suppressUninitReadCheck++
	a.suppressGlobalReadCheck++
	baseType := a.analyzeExpr(base)
	a.suppressGlobalReadCheck--
	a.suppressUninitReadCheck--
	addr, isAddr := baseType.(*AddressSpaceType)
	if !isAddr || addr == nil || addr.Space != "guest" || addr.LayoutName == "" {
		return false
	}
	layout := a.overlayLayouts[addr.LayoutName]
	if layout == nil {
		return false
	}

	offset, width, code, message := resolveOverlayField(layout, field)
	if code != "" {
		a.errorf(stmt.Pos(), "%s: %s", code, message)
		return true
	}
	writeFn, hasFn := overlayWriteFns[width]
	if !hasFn {
		a.errorf(stmt.Pos(), "overlay-field-width-mismatch: field %s.%s has %d-byte width, which has no MemoryManager_WriteU<N> form (expected 1/2/4/8)", layout.Name, field, width)
		return true
	}
	a.checkOverlaySizeGuard(stmt.Pos(), layout, field, overlayCarrierKey(base))

	call := a.buildOverlayWriteCall(target.Position, writeFn, mem, base, offset, stmt.Value)
	stmt.AsOverlayCall = call
	a.analyzeExpr(call)
	return true
}

// buildOverlayWriteCall constructs `writeFn(mem, base + offset, value)` as a CallExpr. The address
// expression is the same `base.cast[uintptr]() + offset` form the read lowering uses; the stored
// value is passed through verbatim (docs/107 §(e) ABI-neutrality).
func (a *Analyzer) buildOverlayWriteCall(pos lexer.Pos, writeFn, mem string, base ast.Expr, offset int64, value ast.Expr) *ast.CallExpr {
	addr := a.overlayAddrExpr(pos, base, offset)
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: writeFn},
		Args:     []ast.Expr{&ast.Ident{Position: pos, Name: mem}, addr, value},
	}
}

// buildOverlayReadCall constructs `readFn(mem, base + offset)` as a CallExpr. The base carrier is
// cast to uintptr so the `+ offset` integer arithmetic matches the MemoryManager address parameter;
// this is the ABI-identical lowering of docs/107 §(e) (a guest carrier lowers to a raw guest
// address, so the cast is a no-op at codegen).
func (a *Analyzer) buildOverlayReadCall(pos lexer.Pos, readFn, mem string, base ast.Expr, offset int64) *ast.CallExpr {
	addr := a.overlayAddrExpr(pos, base, offset)
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: readFn},
		Args:     []ast.Expr{&ast.Ident{Position: pos, Name: mem}, addr},
	}
}

// overlayAddrExpr builds the `base.cast[uintptr]() + offset` address expression shared by overlay
// read and write lowering.
func (a *Analyzer) overlayAddrExpr(pos lexer.Pos, base ast.Expr, offset int64) ast.Expr {
	baseAddr := &ast.CastExpr{
		Position: pos,
		Operand:  base,
		Target:   &ast.NamedType{Position: pos, Name: "uintptr"},
	}
	if offset == 0 {
		return baseAddr
	}
	return &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_PLUS,
		Left:     baseAddr,
		Right:    &ast.IntLit{Position: pos, Value: intToDecimal(offset)},
	}
}

func intToDecimal(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
