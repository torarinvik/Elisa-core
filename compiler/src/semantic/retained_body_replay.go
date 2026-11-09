package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strings"
	"time"
)

type retainedBodyReplayInput struct {
	a                                        *Analyzer
	pre                                      *preBodyState
	constLive                                [][2]*ast.ConstDecl
	addedConstScoped, addedScoped            []scopedDecl
	removedLive                              []*ast.FuncDecl
	newOf                                    func(ast.Decl) ast.Decl
	movedSplices, shifted, shiftedOther      []ast.Decl
	marks                                    map[string][][2]int
	splices                                  []retainedSplice
	plainBodies                              []*retainedPlainBodyRetirement
	plainRebound                             map[*ast.FuncDecl]bool
	resolvedTypeNameRetirement               *resolvedTypeNameRetirementPlan
	functionKeysBeforeReplay                 map[*ast.FuncDecl]string
	bodies                                   [][]ast.Stmt
	scoped                                   map[*ast.FuncDecl]scopedDecl
	plainCoordinates                         *plainCoordinatePlan
	reportDeclarationImport, warningsChanged bool
	start                                    time.Time
}

// retainedReplayBodiesAndDiagnostics installs prepared bodies and preserves unaffected diagnostics.
func (r *RetainedAnalysis) retainedReplayBodiesAndDiagnostics(input retainedBodyReplayInput) ([]scopedDecl, error) {
	a, pre := input.a, input.pre
	constLive, addedConstScoped, addedScoped := input.constLive, input.addedConstScoped, input.addedScoped
	removedLive, newOf, movedSplices, marks, splices := input.removedLive, input.newOf, input.movedSplices, input.marks, input.splices
	plainBodies, plainRebound, resolvedTypeNameRetirement := input.plainBodies, input.plainRebound, input.resolvedTypeNameRetirement
	functionKeysBeforeReplay, bodies, scoped := input.functionKeysBeforeReplay, input.bodies, input.scoped
	shifted, shiftedOther, plainCoordinates := input.shifted, input.shiftedOther, input.plainCoordinates
	reportDeclarationImport, warningsChanged, start := input.reportDeclarationImport, input.warningsChanged, input.start
	// Edited consts are evaluated first, then added functions: the edited bodies use both.
	var changed []scopedDecl
	if len(constLive) != 0 {
		changed = append(changed, r.applyConstEdits(constLive)...)
	}
	changed = append(changed, addedConstScoped...)
	changed = append(changed, addedScoped...)
	drop := map[int]bool{}
	for _, fn := range removedLive {
		if w, ok := r.windows[fn]; ok {
			for k := w[0]; k < w[1]; k++ {
				drop[k] = true
			}
		}
	}
	// Diagnostics sit at pre-edit positions; index the functions before any is shifted.
	owners := retainedDiagnosticOwners(r.decls, append(addedScoped, addedConstScoped...), newOf)
	for _, d := range movedSplices {
		retainedShiftDecl(d, marks)
	}
	spliced := make(map[*ast.FuncDecl]bool, len(splices))
	for _, s := range splices {
		spliced[s.live] = true
	}
	for _, retirement := range plainBodies {
		if err := r.cleanupPlainBodyRetirement(retirement); err != nil {
			return nil, fmt.Errorf("plain body cleanup: %v", err)
		}
		plainRebound[retirement.Product.Owner] = true
	}
	if err := resolvedTypeNameRetirement.apply(a); err != nil {
		r.a = nil
		return nil, fmt.Errorf("resolved type-name body cleanup: %v", err)
	}
	for _, splice := range splices {
		key := functionKeysBeforeReplay[splice.live]
		if pre.prepared != nil {
			prepared := pre.prepared[retainedLineKey(splice.edit.Pos())+"|"+splice.edit.Name]
			if prepared == nil {
				r.a = nil
				return nil, fmt.Errorf("resolved type-name prepared function %s is missing", splice.edit.Name)
			}
			key = pre.keyOf[prepared]
		}
		if key == "" {
			r.a = nil
			return nil, fmt.Errorf("resolved type-name body owner %s is missing", splice.edit.Name)
		}
		rows := pre.resolvedTypeNameRows[key]
		if pre.resolvedTypeNames.functions[key].body != "" && rows == nil {
			r.a = nil
			return nil, fmt.Errorf("resolved type-name body rows for %s are missing", splice.edit.Name)
		}
		if err := importResolvedTypeNameBodyRows(a, rows); err != nil {
			r.a = nil
			return nil, fmt.Errorf("resolved type-name body import: %v", err)
		}
	}
	for i, s := range splices {
		s.live.Body = bodies[i]
		changed = append(changed, scoped[s.live])
	}
	// A body-phase window holds what one function's analysis emitted, including what it
	// emitted for a callee it analyzed first (nested, or in its parallel worker). Each
	// diagnostic goes with the function it lies in: dropped when that one is re-analyzed,
	// kept when it is not, whichever window holds it.
	for fn, w := range r.windows {
		for k := w[0]; k < w[1] && k < len(a.diagnostics); k++ {
			owner := owners.owner(a.diagnostics[k].Pos)
			if owner == nil || owner == fn {
				owner = fn
			}
			if spliced[owner] {
				drop[k] = true
			}
		}
	}
	for _, d := range shifted {
		retainedShiftDecl(d, marks)
		if fn, ok := d.(*ast.FuncDecl); ok {
			a.refreshRetainedFactPositions(a.functionAnalyses[fn], marks)
		}
	}
	for _, d := range shiftedOther {
		retainedShiftDeclOwn(d, marks)
	}
	// Complete producer positions supersede line-only movement inside this
	// closed plain transaction. Fresh replacement bodies already carry positions.
	plainCoordinates.apply()
	if reportDeclarationImport {
		if why := a.importProofPrefix(pre); why != "" {
			// Syntax/types have already been updated. Never reuse a partial
			// revision after a failed source-identity rebind.
			r.a = nil
			return nil, fmt.Errorf("proof report declaration import: %s", why)
		}
	}
	r.LastStats.Diff = time.Since(start)
	// Keep the body-phase diagnostics of unchanged functions; the changed ones
	// and everything the whole-program passes emitted are recomputed.
	// Changed declaration-phase warnings: the live prefix is replaced by the edited program's.
	declPrefix := 0
	if warningsChanged {
		declPrefix = len(r.pre.diags)
		for k := 0; k < declPrefix; k++ {
			drop[k] = true
		}
	}
	kept := make([]Diagnostic, 0, r.bodyEnd)
	if warningsChanged {
		kept = append(kept, pre.diags...)
	}
	// A kept diagnostic may spell a body-local gensym (`__quantifier_<offset>`) in its text;
	// the function it lies in moved by some bytes, and a fresh analysis names it anew.
	for k := 0; k < r.bodyEnd && k < len(a.diagnostics); k++ {
		if !drop[k] {
			d := a.diagnostics[k]
			if strings.Contains(d.Message, "__") {
				if owner, shift := owners.ownerAt(d.Pos); owner != nil {
					d.Message = retainedShiftLocalGensyms(d.Message, shift)
				}
			}
			d.Pos = retainedShiftPos(d.Pos, marks)
			kept = append(kept, d)
		}
	}
	for fn, diags := range r.frameDiags {
		if shift := owners.shiftOf(fn); shift != 0 {
			for i := range diags {
				diags[i].Message = retainedShiftLocalGensyms(diags[i].Message, shift)
			}
		}
	}
	a.diagnostics = kept
	a.reportedDiagnostics = make(map[diagnosticIdentity]bool, len(kept))
	for _, d := range kept {
		a.reportedDiagnostics[diagnosticIdentity{Pos: d.Pos, Severity: d.Severity, Message: d.Message}] = true
	}
	a.warnOnceSeen = nil
	// Windows index into the diagnostics list, which was just compacted; the
	// changed functions get fresh windows below, the rest are re-based.
	r.rebaseWindows(drop)
	if warningsChanged {
		for fn, w := range r.windows {
			r.windows[fn] = [2]int{w[0] + len(pre.diags), w[1] + len(pre.diags)}
		}
	}
	return changed, nil
}
