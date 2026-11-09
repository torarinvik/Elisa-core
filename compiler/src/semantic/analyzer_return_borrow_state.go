package semantic

import "elisacore/src/ast"

// analyzerReturnBorrowState holds the summaries and traversal state used to
// determine whether returned values borrow from their inputs.
type analyzerReturnBorrowState struct {
	// Return-borrow summaries (returnBorrowFlowForFunc), memoized once a function's body has been
	// analyzed: before that its expression types are missing and the summary is conservative.
	returnBorrowFuncAnalyzed  map[*ast.FuncDecl]bool
	returnBorrowFuncSummaries map[*ast.FuncDecl]returnBorrowFlow
	returnBorrowQueryMemo     map[*ast.FuncDecl]returnBorrowQueryEntry
	// Query memos per root function, kept while one function is being analyzed: repeated queries
	// rooted at the same function reuse the callee summaries (returnBorrowRootMemo).
	returnBorrowRootMemos    map[*ast.FuncDecl]map[*ast.FuncDecl]returnBorrowQueryEntry
	returnBorrowRootMemosFor *ast.FuncDecl
	// returnBorrowMayTarget answers per (holder, referent) type pair.
	returnBorrowMayTargetMemo       map[[2]Type]bool
	returnBorrowResultMayTargetMemo map[[2]Type]bool
	returnBorrowCutLog              []*ast.FuncDecl
	// returnBorrowCycleAssume is the current fixpoint approximation of each function whose
	// summary is being computed; a recursive call inside it reads this instead of recursing.
	returnBorrowCycleAssume map[*ast.FuncDecl]returnBorrowFlow
	// returnBorrowLateFrames holds the deferred store checks of functions analyzed while
	// returnBorrowLateEnabled is set; they are evaluated once every body has been analyzed.
	returnBorrowLateFrames   []returnBorrowLateFrame
	returnBorrowLateEnabled  bool
	returnBorrowContentReads bool
	iterBindingSources       map[*Symbol]ast.Expr
	iterBindingByRef         map[*Symbol]bool
	// Deferred local-borrow store checks and the per-binding store environment they are evaluated
	// against (analyzer_return_borrow_store_env.go).
	returnBorrowDeferFrames      []*returnBorrowDeferFrame
	returnBorrowDeclEnv          map[returnBorrowBinderKey]returnBorrowFlow
	returnBorrowDeclRecording    map[returnBorrowBinderKey]returnBorrowFlow
	returnBorrowDeclRecordingFn  *ast.FuncDecl
	returnBorrowDeclChanged      bool
	returnBorrowDeclConservative map[string]bool
	returnBorrowDeclRefLinks     map[string][]string
	// returnBorrowSynthesizedBinders are the binder patterns value comprehensions and single-binder
	// queries synthesize; the return-borrow walker defines them (returnBorrowFlowForComprehension,
	// returnBorrowFlowForQuery).
	returnBorrowSynthesizedBinders   map[ast.Node]bool
	returnBorrowDeclProcessedCalls   map[*ast.CallExpr]bool
	returnBorrowWalkFn               *ast.FuncDecl
	returnBorrowReboundNames         map[*ast.FuncDecl]map[string]bool
	returnProvenanceLocalInProgress  map[*Symbol]bool
	returnBorrowedOwnerRefInProgress map[*ast.FuncDecl]bool
	// inReturnBorrowCapture is true only while computing a return statement's
	// borrowed-owner summary, enabling pass-through tracking of raw reference parameters
	// (so `return p` records "returns param i") without affecting any other analysis.
	inReturnBorrowCapture            bool
	returnBorrowedOwnerLocalProgress map[*Symbol]bool
}
