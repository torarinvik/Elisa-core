package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
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

func (session *IncrementalAnalysisSession) Analyze(file *ast.File, options AnalyzeOptions) *Result {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.stats.Analyses++
	coldFallback := func() *Result {
		session.stats.ColdFallbackAnalyses++
		return AnalyzeWithOptions(file, options)
	}
	if !primitiveResultSchemaValid || !primitiveSchemaValid {
		return coldFallback()
	}
	config, err := json.Marshal(struct {
		Options        AnalyzeOptions
		LinearMutation string
		NoAutoReserve  bool
		OS, Arch       string
	}{options, os.Getenv("ELISA_LINEAR_MUTATION"), autoReserveDisabledSem, runtime.GOOS, runtime.GOARCH})
	if err != nil {
		return coldFallback()
	}
	digest := sha256.Sum256(config)
	invocation := &incrementalBodyInvocation{session: session, keys: map[*ast.FuncDecl]string{}, localInitializers: map[*ast.VarDeclStmt]*primitiveLocalInitializer{}, expressionNodesRemaining: primitiveAnalysisExpressionNodeBudget, config: hex.EncodeToString(digest[:])}
	if session.auditLexicalReads {
		invocation.lexical = &lexicalReadInvocation{indexes: map[*ast.FuncDecl]*FunctionSourceIndex{}}
	}
	invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveConfigKey, Value: invocation.config})
	current := map[incremental.Key]bool{}
	origin := file.Filename
	if len(origin) > primitiveSourceByteBudget {
		return coldFallback()
	}
	if absolute, err := filepath.Abs(origin); err == nil {
		origin = absolute
	}
	// The semantic walker can visit declarations that owner admission skips
	// (for example, a too-long qualified name). Scan the raw declaration tree
	// so pointer aliases across eligible and skipped occurrences also fail closed.
	ownerPointers := primitiveOwnerPointerInventory{}
	if !ownerPointers.scanDeclarations(file.Decls, 0) {
		return coldFallback()
	}
	coldSession := len(session.previous) == 0
	var candidates []primitiveOwnerCandidate
	partialCandidates := false
	expressionBudgetExhausted := false
	if !coldSession {
		var candidatesReady bool
		candidates, candidatesReady = preparePrimitiveOwnerCandidates(file.Decls, origin, session.previous, primitiveOwnerCandidateLimit, invocation.lexical != nil)
		partialCandidates = !candidatesReady
	}
	if partialCandidates {
		hasRetainedPrefix := false
		for _, candidate := range candidates {
			if candidate.retained {
				hasRetainedPrefix = true
				break
			}
		}
		if !hasRetainedPrefix {
			return coldFallback()
		}
	}
	if partialCandidates {
		// Carry prior keys whose owner identity is unmatched in this prefix. They
		// may belong to live suffix owners or to renamed/deleted owners; this
		// bounded census cannot distinguish those cases. Carry them before
		// admission so they still count against the tracked-key cap. Such stale
		// keys may retain capacity until a complete census can retire them.
		scannedPrefixOwners := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			if !candidate.identityValid {
				continue
			}
			name := candidate.name
			if name == "" {
				name = primitiveOwnerCandidateName(candidate.identity)
			}
			scannedPrefixOwners[name] = struct{}{}
		}
		for key := range session.previous {
			if key.Kind != "function-source" {
				current[key] = true
				continue
			}
			ownerName := key.Name
			if localOwner, _, isLocal := strings.Cut(key.Name, "/local-name-v2/"); isLocal {
				ownerName = localOwner
			}
			if _, scanned := scannedPrefixOwners[ownerName]; !scanned {
				current[key] = true
			}
		}
	}
	skipped := 0
	var pendingLocals []*primitiveLocalInitializer
	trimPending := func() {
		available := session.trackedBodyLimit() - len(current)
		if len(pendingLocals) > available {
			skipped += len(pendingLocals) - available
			clear(pendingLocals[available:])
			pendingLocals = pendingLocals[:available]
		}
	}
	collectCandidate := func(candidate primitiveOwnerCandidate, retainedOnly bool) {
		fn, qualifiedName := candidate.function, candidate.qualifiedName
		if retainedOnly != candidate.retained {
			return
		}
		name := ""
		var key incremental.Key
		if candidate.identityValid {
			name = candidate.name
			if name == "" {
				name = primitiveOwnerCandidateName(candidate.identity)
			}
			key = primitiveInput("function-source", name)
			if current[key] {
				return
			}
		}
		auditLexical := invocation.lexical != nil && candidate.auditLexical
		if !candidate.identityValid && !auditLexical {
			// Unsupported owners cannot be cached, so avoid spending the shared
			// return-expression budget on syntax with no remaining admission use.
			return
		}
		if candidate.identityValid && len(current) >= session.trackedBodyLimit() && !auditLexical {
			// No owner slot remains, and this candidate needs no lexical-read
			// observation. Avoid walking its return syntax when it cannot be admitted.
			skipped++
			return
		}
		budgetExhausted := false
		expressions, primitiveReturn := primitiveReturnExpressionsWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
		if budgetExhausted {
			// Candidate discovery must not turn an incomplete owner set into a
			// retirement set. Fall back before publishing any session changes.
			expressionBudgetExhausted = true
			return
		}
		ok := primitiveReturn
		if reads := invocation.lexical; reads != nil && candidate.auditLexical {
			if _, exists := reads.indexes[fn]; !exists && len(reads.indexes) < lexicalReadMaxFunctions {
				readFree := primitiveReturn && len(fn.EnsureValues) == 0 && len(fn.EnsureProofs) == 0
				for _, expr := range expressions {
					if _, ident := expr.(*ast.Ident); ident {
						readFree = false
						break
					}
				}
				var index *FunctionSourceIndex
				if !readFree {
					index, _ = IndexFunctionSource(fn, FunctionIdentityContext{Origin: origin, QualifiedName: qualifiedName})
					if index != nil {
						if len(index.ordinals) > lexicalReadMaxSourceNodes-invocation.lexical.indexNodes {
							index = nil
						} else {
							invocation.lexical.indexNodes += len(index.ordinals)
							session.stats.LexicalSourceIndexesBuilt++
						}
					}
				}
				invocation.lexical.indexes[fn] = index
			}
		}
		if !candidate.identityValid {
			return
		}
		remaining := session.trackedBodyLimit() - len(current)
		if remaining <= 0 || (!ok && remaining < 2) {
			// A successful local-initializer plan always contains at least one
			// local. If it needs two slots, don't build that plan when only one
			// owner slot remains.
			skipped++
			return
		}
		var locals []*primitiveLocalInitializer
		if !ok {
			if invocation.expressionNodesRemaining == 0 {
				expressionBudgetExhausted = true
				return
			}
			budgetExhausted = false
			locals, ok = primitiveLocalInitializersWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
			if !ok {
				if budgetExhausted {
					expressionBudgetExhausted = true
				}
				return
			}
		}
		needed := 1
		if len(locals) > 0 {
			needed = 2
		} // owner context plus at least one initializer
		if len(current)+needed > session.trackedBodyLimit() {
			skipped += 1 + len(locals)
			return
		}
		invocation.keys[fn] = name
		current[key] = true
		trimPending()
		for _, input := range locals {
			identity := sha256.Sum256([]byte(input.declaration.Name))
			input.key = name + "/local-name-v2/" + hex.EncodeToString(identity[:])
			localKey := primitiveInput("function-source", input.key)
			if retainedOnly && !session.previous[localKey] {
				// Reserve space for all other retained owners before new locals.
				if len(current)+len(pendingLocals) >= session.trackedBodyLimit() {
					skipped++
					continue
				}
				pendingLocals = append(pendingLocals, input)
				continue
			}
			if len(current) >= session.trackedBodyLimit() {
				skipped++
				continue
			}
			if !invocation.reserveBinding(input) {
				skipped++
				continue
			}
			invocation.localInitializers[input.declaration] = input
			current[localKey] = true
			trimPending()
		}
		if len(locals) > 0 {
			admitted := false
			for _, input := range locals {
				if input.bindingBytes > 0 {
					admitted = true
					break
				}
			}
			if !admitted {
				delete(invocation.keys, fn)
				delete(current, key)
				skipped++
				kept := pendingLocals[:0]
				for _, pending := range pendingLocals {
					if pending.owner == fn {
						skipped++
					} else {
						kept = append(kept, pending)
					}
				}
				clear(pendingLocals[len(kept):])
				pendingLocals = kept
			}
		}
	}
	collect := func(retainedOnly bool) {
		for _, candidate := range candidates {
			if expressionBudgetExhausted {
				return
			}
			collectCandidate(candidate, retainedOnly)
		}
	}
	// Budget pressure must not turn a large request entirely cold. Retain
	// existing owners first, then fill available slots in declaration order.
	if len(session.previous) > 0 {
		collect(true)
		if expressionBudgetExhausted {
			return coldFallback()
		}
	}
	for _, input := range pendingLocals {
		if len(current) >= session.trackedBodyLimit() {
			skipped++
			continue
		}
		if invocation.keys[input.owner] == "" {
			skipped++
			continue
		}
		if !invocation.reserveBinding(input) {
			skipped++
			continue
		}
		invocation.localInitializers[input.declaration] = input
		current[primitiveInput("function-source", input.key)] = true
	}
	clear(pendingLocals)
	pendingLocals = nil
	localOwners := map[*ast.FuncDecl]bool{}
	for _, input := range invocation.localInitializers {
		localOwners[input.owner] = true
	}
	for fn, name := range invocation.keys {
		if len(fn.Body) > 1 && !localOwners[fn] {
			delete(invocation.keys, fn)
			delete(current, primitiveInput("function-source", name))
			skipped++
		}
	}
	if coldSession {
		// There are no prior owners to prioritize, so prepare and admit new
		// owners directly in occurrence order without retaining a candidate list.
		// The pointer inventory already validated the full tree; normal semantic
		// analysis still checks any declarations beyond these admission caps.
		workBytes := 0
		cacheableRecords := 0
		lexicalOccurrences := 0
		complete := walkPrimitiveOwnerDeclarations(file.Decls, "", 0, len(origin), &workBytes, nil, func(fn *ast.FuncDecl, qualifiedName string) bool {
			if expressionBudgetExhausted {
				return false
			}
			auditLexical := invocation.lexical != nil && lexicalOccurrences < lexicalReadMaxFunctions
			if invocation.lexical != nil {
				lexicalOccurrences++
			}
			if len(current) >= session.trackedBodyLimit() {
				if !auditLexical {
					return false
				}
				// Preserve the bounded lexical prefix without preparing owner
				// identities that cannot be admitted after capacity is full.
				collectCandidate(primitiveOwnerCandidate{
					function: fn, qualifiedName: qualifiedName, auditLexical: true,
				}, false)
				return true
			}
			identity, name, retained, identityValid, ready := preparePrimitiveOwnerCandidateWithBudget(fn, qualifiedName, origin, session.previous, func(amount int) bool {
				return reservePrimitiveOwnerCandidateBytes(&workBytes, amount, primitiveOwnerCandidateWorkBudget)
			})
			if !ready {
				return false
			}
			if identityValid {
				if cacheableRecords >= primitiveOwnerCandidateLimit {
					return false
				}
				cacheableRecords++
			} else if !auditLexical {
				return true
			}
			if !auditLexical {
				qualifiedName = ""
			}
			collectCandidate(primitiveOwnerCandidate{
				function: fn, qualifiedName: qualifiedName, name: name,
				identity: identity, identityValid: identityValid, retained: retained, auditLexical: auditLexical,
			}, false)
			return true
		})
		if expressionBudgetExhausted {
			return coldFallback()
		}
		if !complete {
			session.stats.CapacityFallbackAnalyses++
		}
	} else if partialCandidates {
		if len(invocation.keys) == 0 {
			return coldFallback()
		}
		// Admit only known retained prefix owners on a partial census. This is
		// separate from carrying unmatched prior keys: new owners stay cold even
		// if capacity is available. The semantic analyzer still visits the full
		// file; carried unmatched keys do not authorize suffix replay, and may
		// remain until a complete census can establish that they are dead.
		session.stats.CapacityFallbackAnalyses++
	} else {
		collect(false)
		if expressionBudgetExhausted {
			return coldFallback()
		}
	}
	if session.db == nil {
		session.resetDatabase()
	}
	if skipped > 0 {
		session.stats.CapacityLimitedAnalyses++
		session.stats.CapacitySkippedQueries += uint64(skipped)
	}
	invocation.registerBindingRecipes()
	var removed []incremental.Key
	for key := range session.previous {
		if !current[key] {
			invocation.changes = append(invocation.changes, incremental.Change{Key: key, Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput("function-product", key.Name), Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput("predicate-safety", key.Name), Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput(primitiveBindingRecipeKind, key.Name), Remove: true})
			session.forgetPrimitiveProduct(key.Name)
			removed = append(removed, incremental.Key{Kind: "primitive-return", Name: key.Name}, incremental.Key{Kind: primitiveLocalTypingKind, Name: key.Name}, incremental.Key{Kind: primitiveBindingSourceKind, Name: key.Name})
		}
	}
	session.previous = current
	session.db.Apply(invocation.changes...)
	for _, key := range removed {
		session.db.EvictQuery(key)
	}
	session.retiredQueries += len(removed) / 3
	// Removed products are released immediately. Periodically discard their
	// small key tombstones as well; active program size is not capped here.
	if session.retiredQueries > 4096 {
		session.resetDatabase()
		var live []incremental.Change
		for _, change := range invocation.changes {
			if !change.Remove {
				live = append(live, change)
			}
		}
		session.db.Apply(live...)
		session.retiredQueries = 0
		session.stats.DatabaseResets++
	}
	session.bindingSourceBytes = invocation.bindingBytes
	session.bindingSourceEdges = invocation.bindingEdges
	if len(invocation.keys) == 0 {
		session.stats.ColdFallbackAnalyses++
	}
	defer session.publishPrimitiveProducts()
	result := analyzeWithBodyInvocation(file, options, invocation)
	session.publishPrimitiveProducts()
	invocation.publishEnsureProofs()
	invocation.lexical.publish(session)
	return result
}
func (invocation *incrementalBodyInvocation) prepareAnalyzer(a *Analyzer) {
	// Static impls and named builtin types are complete before body analysis.
	// Local initializer hooks call this for every declaration, so keep the
	// per-analysis operator proof and its input publication out of that loop.
	if invocation.safetyPrepared {
		return
	}
	invocation.safety = "blocked"
	intType, intOK := a.namedTypes["int"].(*BuiltinType)
	i64Type, i64OK := a.namedTypes["i64"].(*BuiltinType)
	if intOK && i64OK && intType.Name == "int" && i64Type.Name == "i64" && a.incrementalOperators.operatorsAbsent(a, intType, i64Type) {
		invocation.safety = primitiveOperatorAbsence
	}
	invocation.session.db.Apply(incremental.Change{Key: primitiveSafetyKey, Value: invocation.safety})
	invocation.safetyPrepared = true
}

func (invocation *incrementalBodyInvocation) prefetchLocalInitializerQueries() {
	if invocation == nil || invocation.session == nil || invocation.safety != primitiveOperatorAbsence || len(invocation.localInitializers) == 0 {
		return
	}
	inputs := make([]*primitiveLocalInitializer, 0, len(invocation.localInitializers))
	for _, input := range invocation.localInitializers {
		// No stored product means no dependency query can hit. Record that miss
		// directly; retained products are batched below and revalidated by the
		// analyzer immediately before consumption.
		product := primitiveInput("function-product", input.key)
		if invocation.session.productBytes[product] == 0 {
			input.queryResult = incremental.EvaluationResult{Err: errPrimitiveProductMiss}
			input.hasQueryResult = true
			continue
		}
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].key < inputs[j].key })
	keys := make([]incremental.Key, len(inputs))
	for i, input := range inputs {
		keys[i] = incremental.Key{Kind: primitiveLocalTypingKind, Name: input.key}
	}
	results := invocation.session.db.EvaluateBatch(context.Background(), keys)
	for i, input := range inputs {
		input.queryResult = results[i]
		input.hasQueryResult = true
	}
}

func primitiveReturnHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	if fn == nil || fn.Name == "" || len(fn.Name) > primitiveSourceByteBudget || len(fn.Body) != 1 {
		return nil, false
	}
	// Every other declaration feature stays cold, including future added fields.
	header := *fn
	if !primitiveParameterSyntax(header.Params) {
		return nil, false
	}
	header.Params = nil // the parser uses a non-nil empty list for `()`
	header.Position = lexer.Pos{}
	header.Name = ""
	header.ReturnType = nil
	header.Body = nil
	if !reflect.ValueOf(header).IsZero() {
		return nil, false
	}
	switch typ := fn.ReturnType.(type) {
	case *ast.NamedType:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	case *ast.BuiltinTypeExpr:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
			return nil, false
		}
		copy.TypeArgs = nil
		copy.ValueArgs = nil
		if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	default:
		return nil, false
	}
	stmt, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok || stmt.Value == nil {
		return nil, false
	}
	statementHeader := *stmt
	statementHeader.Position = lexer.Pos{}
	statementHeader.Value = nil
	if !reflect.ValueOf(statementHeader).IsZero() {
		return nil, false
	}
	return stmt, true
}

const primitiveSourceByteBudget = 1024 * 1024

var primitiveSeenPool = sync.Pool{New: func() any { return make(map[ast.Expr]bool, 256) }}

func primitiveReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	return primitiveReturnExpressionsWithNodeBudget(fn, nil, nil)
}

func primitiveReturnExpressionsWithNodeBudget(fn *ast.FuncDecl, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	if nodes, ok := primitiveI64ReturnExpressionsWithNodeBudget(fn, remainingNodes, budgetExhausted); ok {
		return nodes, true
	}
	if budgetExhausted != nil && *budgetExhausted {
		return nil, false
	}
	return predicateReturnExpressionsWithNodeBudget(fn, remainingNodes, budgetExhausted)
}

// Only return-expression typing is retained here. Plain value postconditions
// are analyzed before this boundary and discharged again by analyzeReturnStatement
// after replay. Their syntax/proof results are never part of the body product.
// Valid inline hints affect the live function signature, not expression typing.
// Keep scoped proof blocks and other header features on the cold path.
func primitiveReturnTypingHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	if fn == nil || len(fn.EnsureProofs) > len(fn.EnsureValues) {
		return nil, false
	}
	for _, proof := range fn.EnsureProofs {
		if proof != nil {
			return nil, false
		}
	}
	if len(fn.Annotations) > 1 {
		return nil, false
	}
	for _, annotation := range fn.Annotations {
		if annotation.Name != "inline" || len(annotation.Args) != 1 {
			return nil, false
		}
		if _, valid := normalizeInlineAnnotationArg(annotation.Args[0]); !valid {
			return nil, false
		}
		copy := annotation
		copy.Position, copy.Name, copy.Args = lexer.Pos{}, "", nil
		if !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	}
	header := *fn
	header.Annotations = nil
	header.EnsureValues = nil
	header.EnsureProofs = nil
	return primitiveReturnHeader(&header)
}

func primitiveI64ReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	return primitiveI64ReturnExpressionsWithNodeBudget(fn, nil, nil)
}

func primitiveI64ReturnExpressionsWithNodeBudget(fn *ast.FuncDecl, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	stmt, ok := primitiveReturnTypingHeader(fn)
	if !ok || len(fn.Name) > primitiveSourceByteBudget {
		return nil, false
	}
	sourceBytes := len(fn.Name)
	var parameters map[string]bool
	if len(fn.Params) > 0 {
		parameters = make(map[string]bool, len(fn.Params))
	}
	for _, param := range fn.Params {
		parameters[param.Name] = true
		sourceBytes += len(param.Name)
	}
	if sourceBytes > primitiveSourceByteBudget {
		return nil, false
	}
	nodes, _, ok := primitiveI64ExpressionNodesWithNodeBudget(stmt.Value, parameters, sourceBytes, remainingNodes, budgetExhausted)
	return nodes, ok
}

// primitiveI64ExpressionNodes validates one expression against an already
// checked set of visible parameter/local names. Local-initializer planning uses
// this directly so each statement doesn't rebuild a synthetic function header
// containing every preceding local. It returns the updated spelling-byte count
// so a multi-initializer plan can enforce one cumulative function budget.
func primitiveI64ExpressionNodes(root ast.Expr, parameters map[string]bool, sourceBytes int) ([]ast.Expr, int, bool) {
	return primitiveI64ExpressionNodesWithNodeBudget(root, parameters, sourceBytes, nil, nil)
}

func primitiveI64ExpressionNodesWithNodeBudget(root ast.Expr, parameters map[string]bool, sourceBytes int, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, int, bool) {
	if root == nil || sourceBytes > primitiveSourceByteBudget {
		return nil, sourceBytes, false
	}
	var nodes []ast.Expr
	seen := primitiveSeenPool.Get().(map[ast.Expr]bool)
	defer func() { clear(seen); primitiveSeenPool.Put(seen) }()
	var visit func(ast.Expr, int) bool
	visit = func(expr ast.Expr, depth int) bool {
		if expr == nil || depth > 128 || len(nodes) >= 4096 {
			return false
		}
		if seen[expr] {
			return false
		}
		if remainingNodes != nil {
			if *remainingNodes <= 0 {
				if budgetExhausted != nil {
					*budgetExhausted = true
				}
				return false
			}
			(*remainingNodes)--
		}
		seen[expr] = true
		nodes = append(nodes, expr)
		switch node := expr.(type) {
		case *ast.Ident:
			if !primitiveParameterSchemaValid || !parameters[node.Name] || len(node.Name) > primitiveSourceByteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Name)
			return true
		case *ast.IntLit:
			if len(node.Value) > primitiveSourceByteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Value)
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			copy.IsHex = false
			return node.Suffix == "" && reflect.ValueOf(copy).IsZero()
		case *ast.ParenExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Inner = nil
			return reflect.ValueOf(copy).IsZero() && visit(node.Inner, depth+1)
		case *ast.UnaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Operand = nil
			return node.Op == lexer.TOKEN_MINUS && reflect.ValueOf(copy).IsZero() && visit(node.Operand, depth+1)
		case *ast.BinaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Left = nil
			copy.Right = nil
			if !reflect.ValueOf(copy).IsZero() || (node.Op != lexer.TOKEN_PLUS && node.Op != lexer.TOKEN_MINUS && node.Op != lexer.TOKEN_STAR) {
				return false
			}
			return visit(node.Left, depth+1) && visit(node.Right, depth+1)
		default:
			return false
		}
	}
	if !visit(root, 0) {
		return nil, sourceBytes, false
	}
	return nodes, sourceBytes, true
}

// The compact source protocol includes every non-position field admitted by
// primitiveReturnExpressions. Fixed node arities make preorder unambiguous;
// values are length-prefixed, and literal spelling/hex mode remain inputs.
func primitiveExpressionDigest(fn *ast.FuncDecl, nodes []ast.Expr) string {
	data := make([]byte, 0, len(nodes)*12+len(fn.Name)+32)
	data = append(data, "canonical-scalar-typing-v3\x00"...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Name)))
	data = append(data, fn.Name...)
	returnName := predicateBuiltinSyntaxName(fn.ReturnType)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(returnName)))
	data = append(data, returnName...)

	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Params)))
	for _, param := range fn.Params {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(param.Name)))
		data = append(data, param.Name...)
		typeName := predicateBuiltinSyntaxName(param.Type)
		data = binary.LittleEndian.AppendUint64(data, uint64(len(typeName)))
		data = append(data, typeName...)

	}
	for _, expr := range nodes {
		switch node := expr.(type) {
		case *ast.Ident:
			data = append(data, 'N')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Name)))
			data = append(data, node.Name...)
		case *ast.IntLit:
			data = append(data, 'I')
			if node.IsHex {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.BoolLit:
			data = append(data, 'T')
			if node.Value {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
		case *ast.CharLit:
			data = append(data, 'C')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.MembershipRangeExpr:
			data = append(data, 'R')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		case *ast.ParenExpr:
			data = append(data, 'P')
		case *ast.UnaryExpr:
			data = append(data, 'U')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		case *ast.BinaryExpr:
			data = append(data, 'B')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		}
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func validPrimitiveTyping(types string) bool {
	// A local product adds one pre-conversion result tag to 4096 expression tags.
	if len(types) == 0 || len(types) > 4097 {
		return false
	}
	for i := range types {
		if types[i] != 'n' && types[i] != 'i' {
			return false
		}
	}
	return true
}

func (a *Analyzer) analyzeIncrementalFunctionBody(fn *ast.FuncDecl, typeArgs []Type) {
	if reads := a.lexicalReads; reads != nil {
		prior := reads.primaryOwner
		reads.primaryOwner = nil
		if !a.suppressDiagnostics && a.convergeDepth == 0 && len(typeArgs) == 0 {
			reads.primaryOwner = fn
		}
		defer func() { reads.primaryOwner = prior }()
	}

	invocation := a.incrementalBodies
	if invocation == nil || a.suppressDiagnostics || a.convergeDepth != 0 || len(typeArgs) != 0 {
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	priorTracking := invocation.tracking
	invocation.tracking = true
	defer func() { invocation.tracking = priorTracking }()
	key, indexed := invocation.keys[fn]
	if !indexed {
		invocation.session.stats.PrimaryBodiesChecked++
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	if indexed {
		invocation.prepareAnalyzer(a)
	} // validate current lookup absence at the reuse boundary
	currentReturn, returnOK := a.currentReturn.(*BuiltinType)
	budgetExhausted := false
	nodes, eligible := primitiveReturnExpressionsWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
	predicate := predicateBuiltinSyntaxName(fn.ReturnType) == "bool"
	returnName := predicateBuiltinSyntaxName(fn.ReturnType)
	bindingsReady := primitiveCurrentParameterBindings(a, fn)
	safetyReady := invocation.safety == primitiveOperatorAbsence
	if predicate {
		returnName = "bool"
		if eligible {
			bindingsReady = predicateCurrentBindingsAndOperators(a, fn, nodes)
			safetyReady = bindingsReady
			safety := "blocked"
			if safetyReady {
				safety = predicateOperatorAbsence
			}
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Value: safety})
		} else if !budgetExhausted {
			bindingsReady, safetyReady = false, false
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Remove: true})
		} else {
			bindingsReady, safetyReady = false, false
		}
	} else if !budgetExhausted {
		invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Remove: true})
	}
	if !eligible || !bindingsReady || !safetyReady || !returnOK || currentReturn.Name != returnName || currentReturn != a.namedTypes[returnName] {
		if indexed && !eligible && !budgetExhausted {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-source", key), Remove: true}, incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.forgetPrimitiveProduct(key)
			invocation.session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: key})
		} else if predicate && (!bindingsReady || !safetyReady) && !budgetExhausted {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.forgetPrimitiveProduct(key)
			invocation.session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: key})
		}
		invocation.session.stats.PrimaryBodiesChecked++
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	session := invocation.session
	if !predicate {
		invocation.prepareEnsureProofMemo(a, fn, key)
	}
	source := primitiveExpressionDigest(fn, nodes)
	sourceChange := incremental.Change{Key: primitiveInput("function-source", key), Value: source}
	session.db.Apply(sourceChange)
	query := incremental.Key{Kind: "primitive-return", Name: key}
	encoded, err := session.db.Evaluate(context.Background(), query)
	if err == nil && encoded != "" {
		if len(encoded) == len(nodes) && ((!predicate && validPrimitiveTyping(encoded)) || (predicate && predicateTypingMatchesNodes(nodes, encoded))) {
			restored := replayPrimitiveParameterNodes(a, nodes, encoded)
			session.stats.PrimaryBodiesReused++
			session.stats.ReusedExpressionNodes += uint64(restored)
			a.visitFunctionBodyWithElementScan(fn.Body, func(stmt ast.Stmt) {
				ret := stmt.(*ast.ReturnStmt)
				a.analyzeReturnStatement(ret, a.exprType(ret.Value))
			}, true)
			return
		}
		// Drop an unusable memo before the cold fallback. Other valid products stay.
		session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
		session.forgetPrimitiveProduct(key)
		session.db.EvictQuery(query)
	}
	session.stats.PrimaryBodiesChecked++
	diagnosticsBefore := len(a.diagnostics)
	a.analyzeFunctionBodyStmts(fn.Body)
	if len(a.diagnostics) != diagnosticsBefore {
		return
	}
	if predicate && !safetyReady {
		return
	}
	budgetExhausted = false
	currentNodes, valid := primitiveReturnExpressionsWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
	if !valid || primitiveExpressionDigest(fn, currentNodes) != source {
		return
	}
	types := make([]byte, len(nodes))
	for i, expr := range nodes {
		if _, rangeNode := expr.(*ast.MembershipRangeExpr); predicate && rangeNode {
			// The marker means exactly "no standalone semantic contribution".
			// Decline if a later analyzer adds one instead of silently losing it.
			_, typed := a.lookupExprType(expr)
			_, facts := a.lookupExprFacts(expr)
			if typed || facts {
				return
			}
			types[i] = predicateRangeTypeTag
			continue
		}
		typ, ok := a.exprType(expr).(*BuiltinType)
		_, parameter := expr.(*ast.Ident)
		if !ok || (!parameter && a.exprFact(expr).HasAnyFacts()) || typ != a.namedTypes[typ.Name] {
			return
		}
		if predicate {
			tag := predicateTypeTag(typ.Name)
			if tag == 0 {
				return
			}
			types[i] = tag
			continue
		}
		switch typ.Name {
		case "int":
			types[i] = 'n'
		case "i64":
			types[i] = 'i'
		default:
			return
		}
	}
	value := primitiveBodyProduct{Source: source, Config: invocation.config, Safety: invocation.safety, Types: string(types)}
	if predicate {
		value.Class = "predicate-v1"
		value.Safety = predicateOperatorAbsence
		if validRangePredicateTyping(value.Types) {
			value.Class = "predicate-range-v1"
		}
		if !predicateTypingMatchesNodes(nodes, value.Types) {
			return
		}
	}
	product, _ := json.Marshal(value)
	// Keep query evaluation lazy after publishing. Reuse validation runs when a
	// later analysis requests this product, avoiding work on the cold analysis.
	session.storePrimitiveProduct(key, string(product))
}
