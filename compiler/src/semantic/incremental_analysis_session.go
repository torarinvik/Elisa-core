package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"errors"
	"sync"
)

// IncrementalAnalysisSession is an opt-in, in-process semantic session. The
// initial body classes are integer returns with current-bound int/i64 parameters.
// It reuses expression typing; parameter reads, return checks, declarations,
// summaries and CFGs stay fresh. Resolved nongeneric mutable-borrow capability
// queries also reuse pointer-free products after declaration shapes are frozen.
// It is not a complete incremental compiler or a persistent whole-body cache.
type IncrementalAnalysisSession struct {
	mu                                           sync.Mutex
	auditLexicalReads                            bool
	lexical                                      *incremental.Session
	capabilities                                 *capabilityQueryCache
	db                                           *incremental.Database
	pendingProductChanges                        []incremental.Change
	pendingProductEvictions                      []incremental.Key
	previous                                     map[incremental.Key]bool
	productBytes                                 map[incremental.Key]int
	retainedProductBytes                         int
	ensureProofs                                 map[string]struct{}
	maxTrackedBodies                             int // zero selects the production default; tests may lower it
	maxRetainedProductBytes                      int // zero selects the production default; tests may lower it
	maxBindingSourceBytes, maxBindingSourceEdges int
	bindingSourceBytes, bindingSourceEdges       int
	stats                                        IncrementalAnalysisStats
	retiredQueries                               int
}
type IncrementalAnalysisStats struct {
	LexicalCapturedObservations, LexicalCapturedFrames                                                                                                       uint64
	LexicalBinderReadsObserved, LexicalBinderReadsDropped, LexicalBinderSelectionsVerified                                                                   uint64
	LexicalSourceIndexesBuilt, LexicalReadsObserved, LexicalSelectionsVerified, LexicalReadsDropped, LexicalVerificationFailures, LexicalPublicationFailures uint64
	LexicalQueriesExecuted, LexicalQueryMemoHits, LexicalQueriesSkipped                                                                                      uint64
	BinderFramesOwned, BinderFramesOwnerless                                                                                                                 uint64
	Analyses, PrimaryBodiesChecked, PrimaryBodiesReused, ReusedExpressionNodes                                                                               uint64
	PrimaryValueChecks, PrimaryExprChecks                                                                                                                    uint64
	PrimaryElementScans                                                                                                                                      uint64
	LocalInitializersChecked, LocalInitializersReused                                                                                                        uint64
	DatabaseResets                                                                                                                                           uint64
	ColdFallbackAnalyses                                                                                                                                     uint64
	CapacityFallbackAnalyses, ProductAdmissionMisses, RetainedProductBytes                                                                                   uint64
	CapacityLimitedAnalyses, CapacitySkippedQueries                                                                                                          uint64
	BindingSpecAdmissionMisses, RetainedBindingSourceBytes, RetainedBindingSourceEdges                                                                       uint64
	EnsureProofMemoHits, EnsureProofMemoPublished                                                                                                            uint64
	Queries                                                                                                                                                  incremental.Stats
	CapabilityReads, CapabilityLocalHits, CapabilityComputed, CapabilityProductsReused, CapabilityProductsPublished, CapabilityProjectionDeclines            uint64
	CapabilityRetainedBytes                                                                                                                                  uint64
	CapabilityQueries                                                                                                                                        incremental.Stats
}
type primitiveBodyProduct struct {
	Source, Config, Safety, Types string
	Class                         string `json:",omitempty"`
}
type incrementalBodyInvocation struct {
	lexical                                    *lexicalReadInvocation
	session                                    *IncrementalAnalysisSession
	keys                                       map[*ast.FuncDecl]string
	changes                                    []incremental.Change
	config, safety                             string
	safetyPrepared                             bool
	tracking                                   bool
	expressionNodesRemaining                   int
	localInitializers                          map[*ast.VarDeclStmt]*primitiveLocalInitializer
	bindingReservedBytes, bindingReservedEdges int
	bindingBytes, bindingEdges                 int
	ensureProofInputs                          map[*ast.FuncDecl]incrementalEnsureProofInput
	ensureProofHits                            map[*ast.FuncDecl]map[int]bool
	pendingEnsureProofs                        map[*ast.FuncDecl]map[int]string
}

var primitiveConfigKey = incremental.Key{Kind: "semantic-config", Name: "primitive-return-v1"}
var primitiveSafetyKey = incremental.Key{Kind: "semantic-lookup-set", Name: "closed-integer-operators-v1"}
var errPrimitiveProductMiss = errors.New("primitive typing product requires cold analysis")

const (
	primitiveSessionMaxTrackedBodies        = 4096
	primitiveSessionMaxRetainedProductBytes = 1 << 20
)

func primitiveInput(kind, name string) incremental.Key {
	return incremental.Key{Kind: kind, Name: name}
}

// IncrementalAnalysisSessionOptions controls development instrumentation. These
// options do not disable any dependency required to authorize typing replay.
type IncrementalAnalysisSessionOptions struct {
	// AuditLexicalReads independently records and verifies ordinary resolver reads.
	// This shadow query world is not consumed by primitive typing replay. Keep it
	// available for qualification without charging every production edit for it.
	AuditLexicalReads bool
}

func NewIncrementalAnalysisSession() *IncrementalAnalysisSession {
	return NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{})
}

func NewIncrementalAnalysisSessionWithOptions(options IncrementalAnalysisSessionOptions) *IncrementalAnalysisSession {
	session := &IncrementalAnalysisSession{previous: map[incremental.Key]bool{}, ensureProofs: map[string]struct{}{}, auditLexicalReads: options.AuditLexicalReads}
	session.resetDatabase()
	return session
}
func (session *IncrementalAnalysisSession) resetDatabase() {
	session.productBytes = make(map[incremental.Key]int)
	session.retainedProductBytes = 0
	session.bindingSourceBytes = 0
	session.bindingSourceEdges = 0
	session.pendingProductChanges = nil
	session.pendingProductEvictions = nil
	session.db = incremental.New(primitiveQueryEvaluators())
}
func (session *IncrementalAnalysisSession) Stats() IncrementalAnalysisStats {
	session.mu.Lock()
	defer session.mu.Unlock()
	stats := session.stats
	stats.RetainedProductBytes = uint64(session.retainedProductBytes)
	stats.RetainedBindingSourceBytes = uint64(session.bindingSourceBytes)
	stats.RetainedBindingSourceEdges = uint64(session.bindingSourceEdges)
	if cache := session.capabilities; cache != nil {
		stats.CapabilityRetainedBytes = uint64(cache.bytes)
		stats.CapabilityQueries = cache.db.Stats()
		stats.CapabilityQueries.Executed += cache.queries.Executed
		stats.CapabilityQueries.MemoHits += cache.queries.MemoHits
		stats.CapabilityQueries.Validated += cache.queries.Validated
		stats.CapabilityQueries.DependencyChecks += cache.queries.DependencyChecks
		stats.CapabilityQueries.InputReads += cache.queries.InputReads
	}
	if session.db != nil {
		stats.Queries = session.db.Stats()
	}
	return stats
}

func (session *IncrementalAnalysisSession) trackedBodyLimit() int {
	if session.maxTrackedBodies > 0 {
		return session.maxTrackedBodies
	}
	return primitiveSessionMaxTrackedBodies
}

func (session *IncrementalAnalysisSession) productByteLimit() int {
	if session.maxRetainedProductBytes > 0 {
		return session.maxRetainedProductBytes
	}
	return primitiveSessionMaxRetainedProductBytes
}

func (session *IncrementalAnalysisSession) forgetPrimitiveProduct(name string) {
	key := primitiveInput("function-product", name)
	if size, ok := session.productBytes[key]; ok {
		if size >= session.retainedProductBytes {
			session.retainedProductBytes = 0
		} else {
			session.retainedProductBytes -= size
		}
		delete(session.productBytes, key)
	}
}

func (session *IncrementalAnalysisSession) storePrimitiveProduct(name string, encoded string) bool {
	productKey := primitiveInput("function-product", name)
	oldSize := session.productBytes[productKey]
	if oldSize > session.retainedProductBytes {
		oldSize = session.retainedProductBytes
	}
	retained := session.retainedProductBytes - oldSize + len(encoded)
	if retained > session.productByteLimit() {
		session.pendingProductChanges = append(session.pendingProductChanges, incremental.Change{Key: productKey, Remove: true})
		session.pendingProductEvictions = append(session.pendingProductEvictions,
			incremental.Key{Kind: "primitive-return", Name: name},
			incremental.Key{Kind: primitiveLocalTypingKind, Name: name},
		)
		session.forgetPrimitiveProduct(name)
		session.stats.ProductAdmissionMisses++
		return false
	}
	session.pendingProductChanges = append(session.pendingProductChanges, incremental.Change{Key: productKey, Value: encoded})
	session.productBytes[productKey] = len(encoded)
	session.retainedProductBytes = retained
	return true
}

// publishPrimitiveProducts commits all products produced during one analysis
// in a single database revision. Their derived queries are intentionally lazy,
// so no body consumes these inputs before this publication point.
func (session *IncrementalAnalysisSession) publishPrimitiveProducts() {
	if len(session.pendingProductChanges) > 0 {
		// A later fallback can forget a product after an earlier path staged
		// its value. Coalesce by key and let the retained-product map decide
		// whether a final Set is still admissible, so publication cannot revive
		// a product that was evicted during this analysis.
		latest := make(map[incremental.Key]incremental.Change, len(session.pendingProductChanges))
		for _, change := range session.pendingProductChanges {
			latest[change.Key] = change
		}
		changes := make([]incremental.Change, 0, len(latest))
		for key, change := range latest {
			if !change.Remove {
				if _, retained := session.productBytes[key]; !retained {
					change.Remove = true
					change.Value = ""
				}
			}
			changes = append(changes, change)
		}
		session.db.Apply(changes...)
		session.pendingProductChanges = nil
	}
	for _, query := range session.pendingProductEvictions {
		session.db.EvictQuery(query)
	}
	session.pendingProductEvictions = nil
}
