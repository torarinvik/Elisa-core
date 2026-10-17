package semantic

import (
	"context"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"fmt"
	"sort"
)

// Read-point snapshots own only the selected-name cells, not mutable Scope maps.
// Symbol descriptors freeze the fields used to derive source slots. Original
// Symbol pointers stay invocation-local for differential checking and rebinding.
type lexicalSymbolObservation struct {
	request  lexicalSymbolRequest
	targets  map[*Symbol]*Symbol
	expected *Symbol
	found    bool
}

func observeLexicalSymbol(request lexicalSymbolRequest, expected *Symbol, found bool) (lexicalSymbolObservation, error) {
	observation := lexicalSymbolObservation{request: request, targets: map[*Symbol]*Symbol{}, expected: expected, found: found}
	seen := map[*Scope]bool{}
	var tail *Scope
	observation.request.Current = nil
	observation.request.Global = nil
	for scope := request.Current; scope != nil; scope = scope.Parent {
		if seen[scope] || len(seen) >= lexicalMaxFrames {
			return lexicalSymbolObservation{}, fmt.Errorf("cyclic/oversized observed scope")
		}
		seen[scope] = true
		frozen := &Scope{Symbols: map[string]*Symbol{}}
		if tail == nil {
			observation.request.Current = frozen
		} else {
			tail.Parent = frozen
		}
		tail = frozen
		if scope == request.Global {
			observation.request.Global = frozen
		}
		if request.LocalOnly && scope == request.Global {
			break
		}
		if symbol, present := scope.Symbols[request.Name]; present {
			var descriptor *Symbol
			if symbol != nil {
				descriptor = &Symbol{Name: symbol.Name, Kind: symbol.Kind, Node: symbol.Node, ParamIndex: symbol.ParamIndex}
				observation.targets[descriptor] = symbol
			}
			frozen.Symbols[request.Name] = descriptor
		}
	}
	return observation, nil
}

// This is query publication/verification, not a body-cache admission certificate.
// All captures are current read-point inputs. A former successful trace is never
// used to invent a future environment. Nothing here survives in Session except
// bounded owned query inputs/products.
type lexicalReadInvocation struct {
	primaryOwner      *ast.FuncDecl
	indexes           map[*ast.FuncDecl]*FunctionSourceIndex
	observations      []lexicalSymbolObservation
	frames            int
	indexNodes        int
	observed, dropped uint64
}

const lexicalReadMaxFunctions = 4096
const lexicalReadMaxFrames = lookupTraceMaxEntries * 4

func (reads *lexicalReadInvocation) observe(a *Analyzer, site *ast.Ident, symbol *Symbol, found bool) {
	if reads == nil || a.suppressDiagnostics || a.currentFuncDecl == nil || reads.primaryOwner != a.currentFuncDecl {
		return
	}
	reads.observed++
	index := reads.indexes[a.currentFuncDecl]
	if index == nil {
		reads.dropped++
		return
	}
	if len(reads.observations) >= lookupTraceMaxEntries {
		reads.dropped++
		return
	}
	request := lexicalSymbolRequest{Index: index, Site: site, Role: "primary-identifier-scope-read", Name: site.Name, Current: a.currentScope, Global: a.globalScope}
	observation, err := observeLexicalSymbol(request, symbol, found)
	if err != nil {
		reads.dropped++
		return
	}
	count := 0
	for scope := observation.request.Current; scope != nil; scope = scope.Parent {
		count++
	}
	if reads.frames+count > lexicalReadMaxFrames {
		reads.dropped++
		return
	}
	reads.frames += count
	reads.observations = append(reads.observations, observation)
}

func (a *Analyzer) lookupIdentifierScope(site *ast.Ident) (*Symbol, bool) {
	symbol, found := a.currentScope.Lookup(site.Name)
	if a.lexicalReads != nil {
		a.lexicalReads.observe(a, site, symbol, found)
	}
	return symbol, found
}

func (reads *lexicalReadInvocation) publish(session *IncrementalAnalysisSession) {
	if reads == nil {
		return
	}
	if session.lexical == nil && len(reads.observations) == 0 {
		session.stats.LexicalReadsObserved += reads.observed
		session.stats.LexicalReadsDropped += reads.dropped
		return
	}
	if session.lexical == nil {
		owner, err := incremental.NewSession(lexicalQueryEvaluators(), lexicalReadSessionLimits())
		if err != nil {
			panic(err)
		} // constant production limits and registry
		session.lexical = owner
	}
	before := session.lexical.Stats().Queries
	prepared := map[incremental.Key]preparedLexicalSymbol{}
	expected := map[incremental.Key]lexicalSymbolObservation{}
	published, err := session.lexical.PublishPrepared(context.Background(), func() ([]incremental.Publication, error) {
		// Validate one function's captured source nodes together. Unsupported owners
		// do not prevent supported functions from contributing their observations.
		groups := map[*FunctionSourceIndex][]lexicalSymbolObservation{}
		for _, observation := range reads.observations {
			groups[observation.request.Index] = append(groups[observation.request.Index], observation)
		}
		var publications []incremental.Publication
		rejected := map[incremental.Key]bool{}
		for _, observations := range groups {
			// Collect all eligible source pointers without making one unsupported
			// declaration or generated site poison unrelated reads in this owner.
			batch := &lexicalAnchorBatch{}
			for _, observation := range observations {
				request := observation.request
				_, _ = batch.add(request.Index, request.Site)
				for scope := request.Current; scope != nil; scope = scope.Parent {
					if symbol, present := scope.Symbols[request.Name]; present && symbol != nil && scope != request.Global {
						if node, ok := symbol.Node.(PositionedFunctionNode); ok {
							_, _ = batch.add(request.Index, node)
						}
					}
				}
			}
			batch.validateAvailable()
			for _, observation := range observations {
				product, err := prepareLexicalSymbol(observation.request, batch)
				if err != nil {
					// Unsupported repeated visits revoke an earlier admissible
					// visit to the same site rather than silently keeping it.
					if anchor, anchorErr := batch.anchor(observation.request.Index, observation.request.Site); anchorErr == nil {
						domain := "symbol"
						if observation.request.LocalOnly {
							domain = "local-symbol"
						}
						if key, keyErr := lexicalSelectionKey(lexicalSelectionSpec{Site: anchor, Role: observation.request.Role, Domain: domain, Name: observation.request.Name}); keyErr == nil {
							delete(prepared, key)
							delete(expected, key)
							rejected[key] = true
						}
					}
					reads.dropped++
					continue
				}
				for id, descriptor := range product.targets {
					product.targets[id] = observation.targets[descriptor]
				}
				key := product.publication.Key
				if rejected[key] {
					reads.dropped++
					continue
				}
				if previous, exists := prepared[key]; exists {
					if !sameLexicalPublication(previous.publication, product.publication) {
						delete(prepared, key)
						delete(expected, key)
						rejected[key] = true
						reads.dropped++
					} else {
						prepared[key] = product
						expected[key] = observation
					}
					continue
				}
				prepared[key] = product
				expected[key] = observation
			}
		}
		keys := make([]incremental.Key, 0, len(prepared))
		for key := range prepared {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Kind != keys[j].Kind {
				return keys[i].Kind < keys[j].Kind
			}
			return keys[i].Name < keys[j].Name
		})
		for _, key := range keys {
			publications = append(publications, prepared[key].publication)
		}
		return publications, nil
	})
	session.stats.LexicalReadsObserved += reads.observed
	session.stats.LexicalReadsDropped += reads.dropped
	if err != nil {
		session.stats.LexicalPublicationFailures++
		return
	}
	session.stats.LexicalQueriesSkipped += uint64(len(published.Skipped))
	skipped := map[incremental.Key]bool{}
	for _, key := range published.Skipped {
		skipped[key] = true
	}
	snapshot := lexicalSymbolSnapshot{revision: published.Revision, requests: prepared}
	verificationFailed := false
	for key, observation := range expected {
		if skipped[key] {
			continue
		}
		symbol, found, err := snapshot.lookup(context.Background(), key)
		if err != nil || symbol != observation.expected || found != observation.found {
			session.stats.LexicalVerificationFailures++
			verificationFailed = true
			continue
		}
		session.stats.LexicalSelectionsVerified++
	}
	if verificationFailed {
		// A differential failure revokes the complete published universe.
		_, _ = session.lexical.Publish(context.Background(), nil)
	}
	after := session.lexical.Stats().Queries
	session.stats.LexicalQueriesExecuted += after.Executed - before.Executed
	session.stats.LexicalQueryMemoHits += after.MemoHits - before.MemoHits
}
func sameLexicalPublication(a, b incremental.Publication) bool {
	if a.Key != b.Key || len(a.Inputs) != len(b.Inputs) {
		return false
	}
	for i, input := range a.Inputs {
		if input != b.Inputs[i] {
			return false
		}
	}
	return true
}

// The lexical world has one query per source read site rather than per distinct
// global name. Keep explicit storage/graph bounds while allowing the real
// project world to fit; throughput takes priority over minimizing resident data.
func lexicalReadSessionLimits() incremental.Limits {
	limits := incremental.DefaultLimits()
	limits.Queries = lookupTraceMaxEntries
	limits.Inputs = lexicalReadMaxFrames + lookupTraceMaxEntries
	limits.InputEdges = limits.Inputs
	limits.Dependencies = limits.Inputs
	limits.Bytes = 256 << 20
	return limits
}
