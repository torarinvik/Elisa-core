package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// checkExternContractDiscipline enforces (under -strict-externs) that every extern function is either
// CONTRACTED (a `requires`/`ensure` boundary contract the call site checks/assumes) or explicitly
// `@trusted("reason")`. An extern that is neither leaves its native boundary unspecified — the call
// site assumes nothing and the native body is checked by nothing — so the discipline flags it. Off
// unless RequireExternContracts (it is NOT implied by -strict, which is on by default; most programs
// link uncontracted libc/host externs and blanket enforcement would break them).
func (a *Analyzer) checkExternContractDiscipline(fn *ast.ExternFuncDecl) {
	if a == nil || fn == nil || !a.requireExternContracts {
		return
	}
	if len(fn.Requires) > 0 || len(fn.EnsureValues) > 0 || len(fn.Ensures) > 0 || externHasTrustedAnnotation(fn) {
		return
	}
	a.errorf(fn.Pos(), "extern function %q has no contract: under -strict-externs every extern must declare a `requires`/`ensure` boundary contract or be annotated `@trusted(\"reason\")` to document why the unverified native body is trusted", fn.Name)
}

// externHasTrustedAnnotation reports whether an extern carries a `@trusted` annotation (its reason is
// validated separately in applyExternFuncAnnotations).
func externHasTrustedAnnotation(fn *ast.ExternFuncDecl) bool {
	for _, ann := range fn.Annotations {
		if ann.Name == "trusted" {
			return true
		}
	}
	return false
}

func (a *Analyzer) applyExternFuncAnnotations(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	if len(fn.Annotations) == 0 {
		a.validateExternSegmentTransitionAnnotation(fn, fnType)
		return
	}
	seen := make(map[string]lexer.Pos, len(fn.Annotations))
	var blockingPos lexer.Pos
	var nonblockingPos lexer.Pos
	for _, annotation := range fn.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on extern function %q (first seen at line %d:%d)", annotation.Name, fn.Name, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if hint, removed := removedAnnotationMigration(annotation.Name); removed {
			a.errorf(annotation.Position, "@%s has been removed; %s", annotation.Name, hint)
			continue
		}
		if !isSupportedExternFunctionAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown extern function annotation @%s on %q", annotation.Name, fn.Name)
			continue
		}
		switch annotation.Name {
		case "link_name":
			if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
				a.errorf(annotation.Position, "@link_name on extern function %q expects exactly one non-empty symbol name", fn.Name)
			}
		case "wasm_import_module":
			if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
				a.errorf(annotation.Position, "@wasm_import_module on extern function %q expects exactly one non-empty interface namespace", fn.Name)
			}
		case "intrinsic":
			if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
				a.errorf(annotation.Position, "@intrinsic on extern function %q expects exactly one non-empty LLVM intrinsic name", fn.Name)
				continue
			}
			intrinsicName := strings.TrimSpace(annotation.Args[0])
			if !strings.HasPrefix(intrinsicName, "llvm.") && !IsElisaBuiltinIntrinsic(intrinsicName) {
				a.errorf(annotation.Position, "@intrinsic on extern function %q expects an LLVM intrinsic name starting with \"llvm.\" or a builtin (elisa.mul_hi.u64, elisa.div_wide.u64, elisa.f64_to_bits, elisa.f64_from_bits)", fn.Name)
				continue
			}
			fnType.IntrinsicName = intrinsicName
		case "callconv":
			if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
				a.errorf(annotation.Position, "@%s on extern function %q expects exactly one calling convention name", annotation.Name, fn.Name)
				continue
			}
			callConv, ok := normalizeExternCallConvAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "unsupported calling convention %q on extern function %q", strings.TrimSpace(annotation.Args[0]), fn.Name)
				continue
			}
			fnType.CallConv = callConv
		case "internal":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@internal on extern function %q does not take arguments", fn.Name)
			}
		case "segment_transition":
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@segment_transition on extern function %q expects exactly one target owner: host or guest", fn.Name)
				continue
			}
			transition, ok := normalizeSegmentTransitionAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@segment_transition on extern function %q uses unsupported target %q (expected host or guest)", fn.Name, strings.TrimSpace(annotation.Args[0]))
				continue
			}
			fnType.SegmentTransition = transition
		case "reentrant_safe":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@reentrant_safe on extern function %q does not take arguments", fn.Name)
				continue
			}
			fnType.HasReentrantSafe = true
		case "blocking":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@blocking on extern function %q does not take arguments", fn.Name)
			}
			blockingPos = annotation.Position
			fnType.PermissionRefs = mergePermissionRefs(fnType.PermissionRefs, []ast.PermissionRef{{Position: annotation.Position, Name: "Blocking", Member: "RawExtern"}})
			fnType.Permissions = permissionFamiliesFromRefs(fnType.PermissionRefs)
		case "nonblocking":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@nonblocking on extern function %q does not take arguments", fn.Name)
			}
			nonblockingPos = annotation.Position
		case "trusted":
			// `@trusted("reason")` documents that an extern is intentionally uncontracted: the native
			// body is assumed to honour an unwritten contract, and the REASON records why that trust is
			// warranted. The reason is mandatory so the trust is never silent (this is what
			// -strict-externs accepts in lieu of an actual `requires`/`ensure`).
			if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
				a.errorf(annotation.Position, "@trusted on extern function %q requires exactly one non-empty reason string explaining why the unverified native body is trusted", fn.Name)
			}
		case "borrows_return":
			a.applyExternBorrowsReturnAnnotation(fn, fnType, annotation)
		}
	}
	if !blockingPos.IsZero() && !nonblockingPos.IsZero() {
		a.errorf(nonblockingPos, "extern function %q cannot be both @blocking and @nonblocking (first @blocking at %s:%d:%d)", fn.Name, blockingPos.File, blockingPos.Line, blockingPos.Col)
	}
	a.validateExternSegmentTransitionAnnotation(fn, fnType)
}

func (a *Analyzer) validateExternSegmentTransitionAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	if permissionRefsTransitionOwner(functionPermissionRefs(fnType)) == segmentOwnerUnknown {
		return
	}
	if fnType.SegmentTransition != FuncSegmentTransitionNone {
		return
	}
	a.errorf(fn.Pos(), "extern function %q mutates the active segment; add @segment_transition(host) or @segment_transition(guest) so the resulting owner is a typed contract, not inferred from permissions", fn.Name)
}

func (a *Analyzer) applyExternVarAnnotations(decl *ast.ExternVarDecl) {
	if decl == nil || len(decl.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(decl.Annotations))
	for _, annotation := range decl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on extern var %q (first seen at %s:%d:%d)", annotation.Name, decl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedExternVarAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown extern var annotation @%s on %q", annotation.Name, decl.Name)
			continue
		}
		if annotation.Name == "link_name" && (len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "") {
			a.errorf(annotation.Position, "@link_name on extern var %q expects exactly one non-empty symbol name", decl.Name)
		}
	}
}

func (a *Analyzer) applyExternTypeAnnotations(decl *ast.ExternTypeDecl, opaque *OpaqueType) {
	if decl == nil || opaque == nil {
		return
	}
	for _, annotation := range decl.Annotations {
		if !isSupportedExternTypeAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unsupported annotation @%s on extern type %q", annotation.Name, decl.Name)
			continue
		}
		switch annotation.Name {
		case "c_opaque":
			if len(annotation.Args) != 2 || strings.TrimSpace(annotation.Args[0]) == "" || strings.TrimSpace(annotation.Args[1]) == "" {
				a.errorf(annotation.Position, "@c_opaque on extern type %q expects header and C type name", decl.Name)
				continue
			}
			opaque.CHeader = strings.TrimSpace(annotation.Args[0])
			opaque.CType = strings.TrimSpace(annotation.Args[1])
		}
	}
}

// applyExternBorrowsReturnAnnotation handles the unified @borrows_return attribute.
// Leading flag tokens select the mode (order-independent): `field` switches the payload
// from a list of parameter paths to return-path/param-path pairs; `rebased` marks the
// borrow as rebased. Everything after the flags is the payload. These four combinations
// replaced the removed @borrows_return_field / _rebased / _field_rebased variants.
func (a *Analyzer) applyExternBorrowsReturnAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	fieldMode, rebased, args := splitBorrowsReturnFlags(annotation.Args)
	if fieldMode {
		a.applyExternBorrowsReturnFieldPayload(fn, fnType, annotation, args, rebased)
		return
	}
	a.applyExternBorrowsReturnPathPayload(fn, fnType, annotation, args, rebased)
}

// splitBorrowsReturnFlags peels the order-independent leading `field` / `rebased` flag
// tokens off a unified @borrows_return argument list, returning the flags and the
// remaining payload args. Shared by the annotation applier and the projection resolver so
// the two never disagree on how the flags are parsed.
func splitBorrowsReturnFlags(args []string) (fieldMode bool, rebased bool, payload []string) {
	payload = args
	for len(payload) > 0 {
		switch strings.ToLower(strings.TrimSpace(payload[0])) {
		case "field":
			fieldMode = true
			payload = payload[1:]
		case "rebased":
			rebased = true
			payload = payload[1:]
		default:
			return fieldMode, rebased, payload
		}
	}
	return fieldMode, rebased, payload
}

func (a *Analyzer) applyExternBorrowsReturnPathPayload(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, args []string, rebased bool) {
	if len(args) == 0 {
		a.errorf(annotation.Position, "@borrows_return on extern function %q expects at least one parameter path", fn.Name)
		return
	}
	var states []regionRefState
	var ownerSummaries []borrowedOwnerRefSummary
	for _, pathText := range args {
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, rebased, "")
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			states = append(states, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			ownerSummaries = append(ownerSummaries, ownerSummary)
		}
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	if len(ownerSummaries) != 0 {
		mergedOwner := cloneBorrowedOwnerRefSummary(ownerSummaries[0])
		for i := 1; i < len(ownerSummaries); i++ {
			if next, ok := mergeBorrowedOwnerRefSummary(mergedOwner, ownerSummaries[i]); ok {
				mergedOwner = next
			}
		}
		fnType.ReturnBorrowedOwnerRefs = mergedOwner
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnFieldPayload(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, args []string, rebased bool) {
	if len(args) == 0 || len(args)%2 != 0 {
		a.errorf(annotation.Position, "@borrows_return(field, …) on extern function %q expects return-path/param-path pairs after the flags", fn.Name)
		return
	}
	if _, ok := a.resolvedStructFields(fnType.Return); !ok {
		a.errorf(annotation.Position, "@borrows_return(field, …) on extern function %q requires a concrete struct return type, got %s", fn.Name, fnType.Return)
		return
	}
	for i := 0; i < len(args); i += 2 {
		returnFieldPath := args[i]
		pathText := args[i+1]
		returnSteps, ok := a.resolveExternReturnTargetPath(fn, fnType, annotation, returnFieldPath)
		if !ok {
			continue
		}
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, rebased, returnFieldPath)
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			fnType.ReturnBorrowedOwnerRefs = assignBorrowedOwnerRefSummaryAtPath(fnType.ReturnBorrowedOwnerRefs, returnSteps, ownerSummary)
		}
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	if !hasBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}
