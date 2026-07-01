//go:build cgo

package semantic

// The `rewrite … as sequence[T]:` expression was removed; build the result
// explicitly with a `darray[T] = []` and a `for` loop that `.push(…)`es each
// element. Rejection is asserted by the parser test
// TestParseSequenceRewriteExprRejected.
