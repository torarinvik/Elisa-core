package semantic

import (
	"context"
	"elisacore/src/incremental"
	"fmt"
)

// One publication owns the complete lexical universe. Current analyzer values
// stay in these invocation-local tables, never in persistent query products.
// Binder source ownership is supplied by the caller; this adapter alone cannot
// authorize body replay. Capture owners outside the source index fail closed.
type lexicalSnapshot struct {
	symbols lexicalSymbolSnapshot
	binders lexicalBinderSnapshot
}

func publishLexicalSnapshot(ctx context.Context, session *incremental.Session, a *Analyzer, symbols []lexicalSymbolRequest, binders []lexicalBinderRequest) (lexicalSnapshot, []incremental.Key, error) {
	var snapshot lexicalSnapshot
	published, err := session.PublishPrepared(ctx, func() ([]incremental.Publication, error) {
		if len(symbols)+len(binders) > lookupTraceMaxEntries {
			return nil, fmt.Errorf("too many combined lexical requests")
		}
		if a == nil && len(binders) != 0 {
			return nil, fmt.Errorf("missing binder analyzer")
		}
		var err error
		var symbolPublications, binderPublications []incremental.Publication
		snapshot.symbols, symbolPublications, err = prepareLexicalSymbols(symbols)
		if err != nil {
			return nil, err
		}
		snapshot.binders, binderPublications, err = prepareLexicalBinders(a, binders)
		if err != nil {
			return nil, err
		}
		return append(symbolPublications, binderPublications...), nil
	})
	if err != nil {
		return lexicalSnapshot{}, nil, err
	}
	snapshot.symbols.revision = published.Revision
	snapshot.binders.revision = published.Revision
	return snapshot, published.Skipped, nil
}
