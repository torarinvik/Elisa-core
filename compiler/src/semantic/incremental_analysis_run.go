package semantic

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
