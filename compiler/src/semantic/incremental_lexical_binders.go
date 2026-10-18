package semantic

import (
	"context"
	"elisacore/src/incremental"
	"fmt"
)

// Sources are outer-to-inner, just like the live analyzer stacks. Each source
// must identify its actual binder owner, including captured enclosing owners.
// Role must equal the binder domain; source owners and map identities must match
// the recorder attached at the actual push. Duplicate source slots decline.
type lexicalBinderSource struct {
	Index *FunctionSourceIndex
	Owner PositionedFunctionNode
	Role  string
}
type lexicalBinderRequest struct {
	Index              *FunctionSourceIndex
	Site               PositionedFunctionNode
	Role, Domain, Name string
	Sources            []lexicalBinderSource
}
type preparedLexicalBinder struct {
	publication incremental.Publication
	targets     map[string]any
}
type lexicalBinderSnapshot struct {
	revision incremental.Revision
	requests map[incremental.Key]preparedLexicalBinder
}

func (a *Analyzer) prepareLexicalBinder(request lexicalBinderRequest, batch *lexicalAnchorBatch) (preparedLexicalBinder, error) {
	result := preparedLexicalBinder{targets: map[string]any{}}
	anchor, err := batch.anchor(request.Index, request.Site)
	if err != nil {
		return result, err
	}
	spec := lexicalSelectionSpec{Site: anchor, Role: request.Role, Domain: request.Domain, Name: request.Name}
	if _, err := validateLexicalSelectionSpec(spec); err != nil {
		return result, err
	}
	count := 0
	switch request.Domain {
	case "type-param":
		count = len(a.typeParamScopes)
	case "const-param":
		count = len(a.constParamScopes)
	case "shape-param":
		count = len(a.shapeParamScopes)
	case "assoc-type":
		count = len(a.interfaceAssocTypeScopes)
	case "param-interface":
		count = len(a.typeParamInterfaceScopes)
	case "permission-param":
		count = len(a.permissionParamScopes)
	case "error-set-param":
		count = len(a.errorSetParamScopes)
	case "region-param":
		count = len(a.regionParamScopes)
	default:
		return result, fmt.Errorf("not a binder lookup")
	}
	if count != len(request.Sources) || count > lexicalMaxFrames {
		return result, fmt.Errorf("binder source/stack mismatch")
	}
	var live []any
	switch request.Domain {
	case "type-param":
		for _, bindings := range a.typeParamScopes {
			live = append(live, bindings)
		}
	case "const-param":
		for _, bindings := range a.constParamScopes {
			live = append(live, bindings)
		}
	case "shape-param":
		for _, bindings := range a.shapeParamScopes {
			live = append(live, bindings)
		}
	case "assoc-type":
		for _, bindings := range a.interfaceAssocTypeScopes {
			live = append(live, bindings)
		}
	case "param-interface":
		for _, bindings := range a.typeParamInterfaceScopes {
			live = append(live, bindings)
		}
	case "permission-param":
		for _, bindings := range a.permissionParamScopes {
			live = append(live, bindings)
		}
	case "error-set-param":
		for _, bindings := range a.errorSetParamScopes {
			live = append(live, bindings)
		}
	case "region-param":
		for _, bindings := range a.regionParamScopes {
			live = append(live, bindings)
		}
	}
	if err := a.lexicalBinders.validate(request.Domain, request.Sources, live); err != nil {
		return result, err
	}
	var frames []lexicalFrameValue
	ids := map[string]bool{}
	for i := count - 1; i >= 0; i-- {
		source := request.Sources[i]
		id, err := lexicalSourceSlot(batch, source.Index, source.Owner, source.Role, request.Name, 0)
		if err != nil {
			return result, err
		}
		if ids[id] {
			return result, fmt.Errorf("binder owners are not distinct")
		}
		ids[id] = true
		frame := lexicalFrameValue{}
		var target any
		switch request.Domain {
		case "type-param":
			value, present := a.typeParamScopes[i][request.Name]
			frame.Present = present
			frame.Nil = present && value == nil
			target = value
		case "const-param":
			value, present := a.constParamScopes[i][request.Name]
			frame.Present = present
			frame.Nil = present && value == nil
			target = value
		case "shape-param":
			value, present := a.shapeParamScopes[i][request.Name]
			frame.Present = present
			frame.Nil = present && value == nil
			target = value
		case "assoc-type":
			value, present := a.interfaceAssocTypeScopes[i][request.Name]
			frame.Present = present
			frame.Nil = present && value == nil
			target = value
		case "param-interface":
			value, present := a.typeParamInterfaceScopes[i][request.Name]
			frame.Present = present
			frame.Nil = present && value == nil
			target = value
		case "permission-param":
			frame.Present = a.permissionParamScopes[i][request.Name]
			target = true
		case "error-set-param":
			frame.Present = a.errorSetParamScopes[i][request.Name]
			target = true
		case "region-param":
			frame.Present = a.regionParamScopes[i][request.Name]
			target = true
		}
		if frame.Present && !frame.Nil {
			frame.Binding = id
			result.targets[id] = target
		}
		frames = append(frames, frame)
	}
	result.publication, err = lexicalPublication(spec, frames)
	return result, err
}
func prepareLexicalBinders(a *Analyzer, requests []lexicalBinderRequest) (lexicalBinderSnapshot, []incremental.Publication, error) {
	snapshot := lexicalBinderSnapshot{requests: map[incremental.Key]preparedLexicalBinder{}}
	publications, err := func() ([]incremental.Publication, error) {
		if len(requests) > lookupTraceMaxEntries {
			return nil, fmt.Errorf("too many binder requests")
		}
		batch := &lexicalAnchorBatch{}
		work := 0
		for _, request := range requests {
			anchor, err := batch.add(request.Index, request.Site)
			if err != nil {
				return nil, err
			}
			if _, err := validateLexicalSelectionSpec(lexicalSelectionSpec{Site: anchor, Role: request.Role, Domain: request.Domain, Name: request.Name}); err != nil {
				return nil, err
			}
			work += len(request.Sources)
			if len(request.Sources) > lexicalMaxFrames || work > lookupTraceMaxEntries*4 {
				return nil, fmt.Errorf("binder preparation budget exceeded")
			}
			for _, source := range request.Sources {
				if _, err := batch.add(source.Index, source.Owner); err != nil {
					return nil, err
				}
			}
		}
		if err := batch.validate(); err != nil {
			return nil, err
		}
		var publications []incremental.Publication
		for _, request := range requests {
			prepared, err := a.prepareLexicalBinder(request, batch)
			if err != nil {
				return nil, err
			}
			key := prepared.publication.Key
			if _, exists := snapshot.requests[key]; exists {
				return nil, fmt.Errorf("duplicate binder lookup context")
			}
			snapshot.requests[key] = prepared
			publications = append(publications, prepared.publication)
		}
		return publications, nil
	}()
	return snapshot, publications, err
}
func publishLexicalBinders(ctx context.Context, session *incremental.Session, a *Analyzer, requests []lexicalBinderRequest) (lexicalBinderSnapshot, []incremental.Key, error) {
	var snapshot lexicalBinderSnapshot
	published, err := session.PublishPrepared(ctx, func() ([]incremental.Publication, error) {
		var publications []incremental.Publication
		var err error
		snapshot, publications, err = prepareLexicalBinders(a, requests)
		return publications, err
	})
	if err != nil {
		return lexicalBinderSnapshot{}, nil, err
	}
	snapshot.revision = published.Revision
	return snapshot, published.Skipped, nil
}
func (snapshot lexicalBinderSnapshot) lookup(ctx context.Context, key incremental.Key) (any, bool, error) {
	prepared, ok := snapshot.requests[key]
	if !ok {
		return nil, false, fmt.Errorf("binder lookup not prepared")
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
	target, ok := prepared.targets[product.Binding]
	if !ok {
		return nil, false, fmt.Errorf("selected binder value is not current")
	}
	return target, product.Found, nil
}
