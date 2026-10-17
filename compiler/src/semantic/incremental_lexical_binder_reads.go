package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"fmt"
	"reflect"
)

// Selection captures retain current Type objects only inside this invocation.
// The portable product identifies the source slot, not the resolved Type value.
type lexicalCapturedTypeBinder struct {
	present bool
	value   Type
}
type lexicalBinderObservation struct {
	request  lexicalBinderRequest
	frames   []lexicalCapturedTypeBinder // outer-to-inner
	expected Type
	found    bool
}

func (a *Analyzer) lookupTypeParamAt(site *ast.NamedType) (Type, bool) {
	value, found := a.lookupTypeParam(site.Name)
	if a.lexicalReads != nil {
		a.lexicalReads.observeTypeBinder(a, site, "type-param", value, found)
	}
	return value, found
}
func (a *Analyzer) lookupInterfaceAssocTypeAt(site *ast.NamedType) (Type, bool) {
	value, found := a.lookupInterfaceAssocType(site.Name)
	if a.lexicalReads != nil {
		a.lexicalReads.observeTypeBinder(a, site, "assoc-type", value, found)
	}
	return value, found
}
func (reads *lexicalReadInvocation) observeTypeBinder(a *Analyzer, site *ast.NamedType, domain string, value Type, found bool) {
	if reads == nil || a.suppressDiagnostics || a.currentFuncDecl == nil || reads.primaryOwner != a.currentFuncDecl {
		return
	}
	reads.bindersObserved++
	index := reads.indexes[a.currentFuncDecl]
	if index == nil || len(reads.observations)+len(reads.binders) >= lexicalReadMaxObservations {
		reads.bindersDropped++
		return
	}
	var scopes []map[string]Type
	switch domain {
	case "type-param":
		scopes = a.typeParamScopes
	case "assoc-type":
		scopes = a.interfaceAssocTypeScopes
	default:
		reads.bindersDropped++
		return
	}
	if len(scopes) > lexicalMaxFrames || reads.frames+len(scopes) > lexicalReadMaxFrames {
		reads.bindersDropped++
		return
	}
	request := lexicalBinderRequest{Index: index, Site: site, Role: "primary-named-type-read", Domain: domain, Name: site.Name}
	var live []any
	if len(scopes) > 0 {
		if a.lexicalBinders == nil || len(a.lexicalBinders.frames[domain]) != len(scopes) {
			reads.bindersDropped++
			return
		}
		for i, frame := range a.lexicalBinders.frames[domain] {
			owner, ok := frame.owner.(PositionedFunctionNode)
			if !ok {
				reads.bindersDropped++
				return
			}
			ownerIndex := index
			if function, ok := frame.owner.(*ast.FuncDecl); ok {
				ownerIndex = reads.indexes[function]
			}
			if ownerIndex == nil {
				reads.bindersDropped++
				return
			}
			request.Sources = append(request.Sources, lexicalBinderSource{Index: ownerIndex, Owner: owner, Role: domain})
			live = append(live, scopes[i])
		}
	}
	if err := a.lexicalBinders.validate(domain, request.Sources, live); err != nil {
		reads.bindersDropped++
		return
	}
	observation := lexicalBinderObservation{request: request, expected: value, found: found}
	for _, bindings := range scopes {
		target, present := bindings[site.Name]
		observation.frames = append(observation.frames, lexicalCapturedTypeBinder{present, target})
	}
	reads.frames += len(scopes)
	reads.binders = append(reads.binders, observation)
}

func (observation lexicalBinderObservation) key(batch *lexicalAnchorBatch) (incremental.Key, error) {
	anchor, err := batch.anchor(observation.request.Index, observation.request.Site)
	if err != nil {
		return incremental.Key{}, err
	}
	return lexicalSelectionKey(lexicalSelectionSpec{Site: anchor, Role: observation.request.Role, Domain: observation.request.Domain, Name: observation.request.Name})
}
func (observation lexicalBinderObservation) prepare(batch *lexicalAnchorBatch) (preparedLexicalBinder, error) {
	request := observation.request
	result := preparedLexicalBinder{targets: map[string]any{}}
	anchor, err := batch.anchor(request.Index, request.Site)
	if err != nil {
		return result, err
	}
	if len(observation.frames) != len(request.Sources) {
		return result, fmt.Errorf("captured binder frame mismatch")
	}
	var frames []lexicalFrameValue
	ids := map[string]bool{}
	for i := len(observation.frames) - 1; i >= 0; i-- {
		source := request.Sources[i]
		id, err := lexicalSourceSlot(batch, source.Index, source.Owner, source.Role, request.Name, 0)
		if err != nil {
			return result, err
		}
		if ids[id] {
			return result, fmt.Errorf("captured binder source slots collide")
		}
		ids[id] = true
		captured := observation.frames[i]
		frame := lexicalFrameValue{Present: captured.present, Nil: captured.present && captured.value == nil}
		if frame.Present && !frame.Nil {
			frame.Binding = id
			result.targets[id] = captured.value
		}
		frames = append(frames, frame)
	}
	result.publication, err = lexicalPublication(lexicalSelectionSpec{Site: anchor, Role: request.Role, Domain: request.Domain, Name: request.Name}, frames)
	return result, err
}

// Capture ownership was checked against live maps at the actual read. At
// publication only source liveness is rechecked; popped/mutated current scopes
// cannot replace those frozen name cells.
func (reads *lexicalReadInvocation) prepareBinders() (map[incremental.Key]preparedLexicalBinder, map[incremental.Key]lexicalBinderObservation) {
	prepared := map[incremental.Key]preparedLexicalBinder{}
	expected := map[incremental.Key]lexicalBinderObservation{}
	batch := &lexicalAnchorBatch{}
	for _, observation := range reads.binders {
		request := observation.request
		batch.add(request.Index, request.Site)
		for _, source := range request.Sources {
			batch.add(source.Index, source.Owner)
		}
	}
	batch.validateAvailable()
	rejected := map[incremental.Key]bool{}
	for _, observation := range reads.binders {
		key, err := observation.key(batch)
		if err != nil {
			reads.bindersDropped++
			continue
		}
		product, err := observation.prepare(batch)
		if err != nil || rejected[key] {
			delete(prepared, key)
			delete(expected, key)
			rejected[key] = true
			reads.bindersDropped++
			continue
		}
		if previous, exists := prepared[key]; exists && !sameLexicalPublication(previous.publication, product.publication) {
			delete(prepared, key)
			delete(expected, key)
			rejected[key] = true
			reads.bindersDropped++
			continue
		}
		prepared[key] = product
		expected[key] = observation
	}
	return prepared, expected
}

func sameLexicalBinderValue(value any, expected Type) bool {
	if value == nil || expected == nil {
		return value == nil && expected == nil
	}
	return reflect.TypeOf(value) == reflect.TypeOf(expected) && reflect.ValueOf(value).Comparable() && value == expected
}
