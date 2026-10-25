package semantic

import (
	"fmt"
	"reflect"
)

// Construction-only immutable baseline. Inherited slice headers use the exact
// old constructor cap policy, while locally computed facts keep their OWN cap.
// The source consumer only reads scalar/opaque fields and ranges reach.
type analyzerStoreScanParamFactsOverlay struct {
	base map[storeScanParamKey]storeScanParamFacts
}

func (a *Analyzer) lookupStoreScanParamFacts(key storeScanParamKey) (storeScanParamFacts, bool) {
	if facts, present := a.storeScanParamFactsCache[key]; present {
		return facts, true
	}
	overlay := a.storeScanParamFactsOverlay
	if overlay == nil {
		return storeScanParamFacts{}, false
	}
	if !specializationParamFactsCopySchemaValid {
		a.rejectStoreScanParamFactsOverlayOperation("unknown parameter-facts value schema")
		return storeScanParamFacts{}, false
	}
	facts, present := overlay.base[key]
	if !present {
		return storeScanParamFacts{}, false
	}
	if facts.reach != nil {
		facts.reach = facts.reach[:len(facts.reach):len(facts.reach)]
	}
	return facts, true
}

func (a *Analyzer) setStoreScanParamFacts(key storeScanParamKey, facts storeScanParamFacts) {
	if a.storeScanParamFactsCache == nil {
		a.storeScanParamFactsCache = map[storeScanParamKey]storeScanParamFacts{}
	}
	a.storeScanParamFactsCache[key] = facts
}

// Explicit bool is the SAME closed four-field admission used by the constructor;
// it also makes the unknown-schema full-reference path testable without racing
// on or mutating the package's immutable-after-init schema verdict.
func cloneStoreScanParamFactsConstructionMap(source map[storeScanParamKey]storeScanParamFacts, schemaValid bool) map[storeScanParamKey]storeScanParamFacts {
	if !schemaValid {
		return deepCloneValue(reflect.ValueOf(source)).Interface().(map[storeScanParamKey]storeScanParamFacts)
	}
	if source == nil {
		return nil
	}
	return map[storeScanParamKey]storeScanParamFacts{}
}

func cloneAnalyzerStoreScanParamFactsOverlay(source *analyzerStoreScanParamFactsOverlay) *analyzerStoreScanParamFactsOverlay {
	if source == nil {
		return nil
	}
	return &analyzerStoreScanParamFactsOverlay{base: source.base}
}

func (a *Analyzer) rejectStoreScanParamFactsOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization store-scan parameter-facts overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
