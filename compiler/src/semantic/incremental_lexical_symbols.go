package semantic

import (
	"context"
	"elisacore/src/incremental"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type lexicalSymbolRequest struct {
	Index           *FunctionSourceIndex
	Site            PositionedFunctionNode
	Role, Name      string
	Current, Global *Scope
	LocalOnly       bool
}
type preparedLexicalSymbol struct {
	publication incremental.Publication
	targets     map[string]*Symbol
}

// A source binding slot is position-independent and distinct from the binding's
// mutable type/flow properties. This constructor uses only validated source
// anchors; generated or foreign declarations need their own explicit recipes.
func lexicalSourceSlot(batch *lexicalAnchorBatch, index *FunctionSourceIndex, node PositionedFunctionNode, role, name string, ordinal int) (string, error) {
	anchor, err := batch.anchor(index, node)
	if err != nil {
		return "", err
	}
	if role == "" || name == "" || ordinal < 0 || !utf8.ValidString(role) || !utf8.ValidString(name) || len(role)+len(name)+len(anchor.FunctionKey)+len(anchor.Kind) > (lookupSelectionMaxBytes/6-512)/6 {
		return "", fmt.Errorf("invalid source binding slot")
	}
	data, err := json.Marshal(struct {
		Anchor     FunctionSourceAnchor
		Role, Name string
		Ordinal    int
	}{anchor, role, name, ordinal})
	if err != nil || len(data) > lookupSelectionMaxBytes/6 {
		return "", fmt.Errorf("binding identity budget exceeded")
	}
	return string(data), nil
}
func prepareLexicalSymbol(request lexicalSymbolRequest, batch *lexicalAnchorBatch) (preparedLexicalSymbol, error) {
	result := preparedLexicalSymbol{targets: map[string]*Symbol{}}
	anchor, err := batch.anchor(request.Index, request.Site)
	if err != nil {
		return result, err
	}
	domain := "symbol"
	if request.LocalOnly {
		domain = "local-symbol"
	}
	spec := lexicalSelectionSpec{Site: anchor, Role: request.Role, Domain: domain, Name: request.Name}
	if _, err := validateLexicalSelectionSpec(spec); err != nil {
		return result, err
	}
	var frames []lexicalFrameValue
	seen := map[*Scope]bool{}
	for scope := request.Current; scope != nil; scope = scope.Parent {
		if request.LocalOnly && scope == request.Global {
			break
		}
		if seen[scope] || len(frames) >= lexicalMaxFrames {
			return result, fmt.Errorf("cyclic/oversized lexical scope chain")
		}
		seen[scope] = true
		symbol, present := scope.Symbols[request.Name]
		frame := lexicalFrameValue{Present: present, Nil: present && symbol == nil}
		if present && symbol != nil {
			binding := ""
			if scope == request.Global {
				// This is a current global slot, not a nominal declaration/type identity.
				data, _ := json.Marshal([]string{"global-symbol-slot-v1", request.Name})
				binding = string(data)
			} else {
				node, ok := symbol.Node.(PositionedFunctionNode)
				if !ok {
					return result, fmt.Errorf("local symbol lacks source declaration")
				}
				ordinal := 0
				role := "local/" + string(symbol.Kind)
				if symbol.Kind == SymbolParam {
					if symbol.Node != request.Index.root || symbol.ParamIndex < 0 || symbol.ParamIndex >= len(request.Index.root.Params) || request.Index.root.Params[symbol.ParamIndex].Name != symbol.Name {
						return result, fmt.Errorf("parameter binding does not match current declaration")
					}
					ordinal = symbol.ParamIndex
					role = "parameter"
				}
				binding, err = lexicalSourceSlot(batch, request.Index, node, role, symbol.Name, ordinal)
				if err != nil {
					return result, err
				}
			}
			if old, exists := result.targets[binding]; exists && old != symbol {
				return result, fmt.Errorf("ambiguous current binding slot")
			}
			frame.Binding = binding
			result.targets[binding] = symbol
		}
		frames = append(frames, frame)
	}
	result.publication, err = lexicalPublication(spec, frames)
	return result, err
}

// The revision and rebinding tables are created together. They cannot be mixed
// with tables from another analysis, even if portable selection output is equal.
// Only this invocation object owns Symbol pointers; the persistent Session does
// not retain it. Flow facts and current Type reads still execute independently.
type lexicalSymbolSnapshot struct {
	revision incremental.Revision
	requests map[incremental.Key]preparedLexicalSymbol
}

func prepareLexicalSymbols(requests []lexicalSymbolRequest) (lexicalSymbolSnapshot, []incremental.Publication, error) {
	snapshot := lexicalSymbolSnapshot{requests: map[incremental.Key]preparedLexicalSymbol{}}
	publications, err := func() ([]incremental.Publication, error) {
		if len(requests) > lookupTraceMaxEntries {
			return nil, fmt.Errorf("too many lexical requests")
		}
		batch := &lexicalAnchorBatch{}
		work := 0
		for _, request := range requests {
			anchor, err := batch.add(request.Index, request.Site)
			if err != nil {
				return nil, err
			}
			domain := "symbol"
			if request.LocalOnly {
				domain = "local-symbol"
			}
			if _, err := validateLexicalSelectionSpec(lexicalSelectionSpec{Site: anchor, Role: request.Role, Domain: domain, Name: request.Name}); err != nil {
				return nil, err
			}
			seen := map[*Scope]bool{}
			for scope := request.Current; scope != nil; scope = scope.Parent {
				if request.LocalOnly && scope == request.Global {
					break
				}
				work++
				if work > lookupTraceMaxEntries*4 || seen[scope] || len(seen) >= lexicalMaxFrames {
					return nil, fmt.Errorf("lexical scope preparation budget exceeded")
				}
				seen[scope] = true
				if symbol, present := scope.Symbols[request.Name]; present && symbol != nil && scope != request.Global {
					node, ok := symbol.Node.(PositionedFunctionNode)
					if !ok {
						return nil, fmt.Errorf("unanchored local symbol")
					}
					if _, err := batch.add(request.Index, node); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := batch.validate(); err != nil {
			return nil, err
		}
		var publications []incremental.Publication
		for _, request := range requests {
			prepared, err := prepareLexicalSymbol(request, batch)
			if err != nil {
				return nil, err
			}
			key := prepared.publication.Key
			if _, exists := snapshot.requests[key]; exists {
				return nil, fmt.Errorf("duplicate lexical site/context requires explicit disambiguation")
			}
			snapshot.requests[key] = prepared
			publications = append(publications, prepared.publication)
		}
		return publications, nil
	}()
	return snapshot, publications, err
}
func publishLexicalSymbols(ctx context.Context, session *incremental.Session, requests []lexicalSymbolRequest) (lexicalSymbolSnapshot, []incremental.Key, error) {
	var snapshot lexicalSymbolSnapshot
	published, err := session.PublishPrepared(ctx, func() ([]incremental.Publication, error) {
		var publications []incremental.Publication
		var err error
		snapshot, publications, err = prepareLexicalSymbols(requests)
		return publications, err
	})
	if err != nil {
		return lexicalSymbolSnapshot{}, nil, err
	}
	snapshot.revision = published.Revision
	return snapshot, published.Skipped, nil
}
func (snapshot lexicalSymbolSnapshot) lookup(ctx context.Context, key incremental.Key) (*Symbol, bool, error) {
	current, ok := snapshot.requests[key]
	if !ok {
		return nil, false, fmt.Errorf("lookup not prepared in this invocation")
	}
	encoded, err := snapshot.revision.Evaluate(ctx, key)
	if err != nil {
		return nil, false, err
	}
	var product lexicalSelectionProduct
	if err := decodeLookupSelection(encoded, &product); err != nil {
		return nil, false, err
	}
	if !product.Present {
		return nil, false, nil
	}
	if product.Nil {
		return nil, product.Found, nil
	}
	symbol, ok := current.targets[product.Binding]
	if !ok || symbol == nil {
		return nil, false, fmt.Errorf("selected source binding is not current")
	}
	return symbol, product.Found, nil
}
