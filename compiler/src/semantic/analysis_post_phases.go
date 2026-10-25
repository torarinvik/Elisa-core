package semantic

import "elisacore/src/ast"

// Phase order is shared by ordinary analysis, retained updates and disk
// bootstrap. The initial build can additionally checkpoint the first phase
// between complete late return-borrow frame checks.
var analysisPostPhaseNames = [...]string{
	"evaluateLateReturnBorrowFrames", "checkProtocolDefaultMethodContracts",
	"inferFunctionPermissionEffects", "validateDeferredBorrowedNurseryShutdownCalls",
	"checkDeferredImplEffects", "validateProgressSafety", "validatePermissionUsage",
	"analyzeExports", "finalizeCallArgDisjoint", "finalizeFuncDisjointParams",
	"lintHotKernelsForDisjointness",
}

func (a *Analyzer) runPostPhase(phase int, decls []scopedDecl, options AnalyzeOptions, solver func(*Analyzer, []scopedDecl), appended ...map[*ast.FuncDecl]*FuncType) {
	postTimed(analysisPostPhaseNames[phase], func() {
		switch phase {
		case 0:
			if a.retained != nil {
				a.retained.lateFrames = append(a.retained.lateFrames[:0], a.returnBorrowLateFrames...)
			}
			a.evaluateLateReturnBorrowFrames()
		case 1:
			a.clearProofPhase(proofPhasePostDefault)
			// Protocol defaults must satisfy their own contracts after body analysis.
			a.checkProtocolDefaultMethodContracts(decls)
		case 2:
			if solver == nil {
				a.inferFunctionPermissionEffects(decls, appended...)
			} else {
				solver(a, decls)
			}
		case 3:
			a.validateDeferredBorrowedNurseryShutdownCalls()
		case 4:
			for _, check := range a.deferredImplEffectChecks {
				check.run(a)
			}
			// Retained updates solve these same method rows again. Keep the
			// obligations so their post-body diagnostics are reproduced too.
		case 5:
			if options.EnforceProgressSafety {
				a.validateProgressBlocking(decls)
				a.validateProgressRecursion(decls)
			}
		case 6:
			a.validatePermissionUsage(decls)
		case 7:
			a.analyzeExports(decls)
		case 8:
			a.finalizeCallArgDisjoint()
		case 9:
			a.finalizeFuncDisjointParams(decls)
		case 10:
			a.lintHotKernelsForDisjointness()
		}
	})
}
