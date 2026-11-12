package semantic

// One publication owns the complete lexical universe. Current analyzer values
// stay in these invocation-local tables, never in persistent query products.
// Binder sources must match push-time recorded owners and live map identities.
// This adapter alone cannot authorize body replay. Capture owners outside the
// source index fail closed.
type lexicalSnapshot struct {
	symbols lexicalSymbolSnapshot
	binders lexicalBinderSnapshot
}
