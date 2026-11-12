package semantic

import (
	"elisacore/src/incremental"
)

func lookupQueryEvaluators() map[string]incremental.Evaluator {
	return map[string]incremental.Evaluator{lookupSelectionQueryKind: evaluateLookupSelection, lookupAmbiguityQueryKind: evaluateLookupAmbiguity}
}
