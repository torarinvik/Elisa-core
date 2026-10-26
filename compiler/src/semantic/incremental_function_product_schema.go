package semantic

import (
	"fmt"
	"reflect"
)

// Disposition describes work a complete function-product adapter must perform.
// A classification is a requirement, never evidence that replay is implemented.
type FunctionProductDisposition uint8

const (
	FunctionProductInvocation    FunctionProductDisposition = 1 << iota // current invocation only
	FunctionProductGlobal                                               // rebuilt current declaration/type environment
	FunctionProductRebind                                               // restore product contribution on current nodes/types
	FunctionProductAggregate                                            // recompute downstream interprocedural/global result
	FunctionProductAnalyzerState                                        // hydrate live-analyzer APIs and deferred contributions
)
const functionProductAllDispositions = FunctionProductInvocation | FunctionProductGlobal | FunctionProductRebind | FunctionProductAggregate | FunctionProductAnalyzerState

type FunctionProductRequirement struct {
	Field       string
	Disposition FunctionProductDisposition
}

// Explicit inventory: new semantic Result fields must not silently disappear
// from a reused body. Keep this list exhaustive rather than guessing from types.
var functionProductFieldPolicies = map[string]FunctionProductDisposition{
	"PermissionEffectsConverged": FunctionProductAggregate,

	"SMTProfile":              FunctionProductInvocation,
	"EnforcePerfLints":        FunctionProductInvocation,
	"File":                    FunctionProductInvocation,
	"LoweredFile":             FunctionProductInvocation,
	"GlobalScope":             FunctionProductGlobal,
	"ModuleAliases":           FunctionProductGlobal,
	"NamedTypes":              FunctionProductGlobal,
	"StaticInterfaces":        FunctionProductGlobal,
	"StaticImpls":             FunctionProductGlobal,
	"ConstValues":             FunctionProductGlobal,
	"ExprTypes":               FunctionProductRebind,
	"PermGrowthOps":           FunctionProductRebind,
	"RewriteDefaults":         FunctionProductRebind,
	"OptionalBindSourceTypes": FunctionProductRebind,
	"InterfaceMethodRefs":     FunctionProductRebind,
	"SafeCalls":               FunctionProductRebind,
	"ExprFacts":               FunctionProductRebind,
	"CastHooks":               FunctionProductRebind,
	"InitCalls":               FunctionProductRebind,
	"PostfixShorthandCalls":   FunctionProductRebind,
	"RegionStacks":            FunctionProductRebind,
	"DeathTimeCohorts":        FunctionProductAggregate,
	"DeathTimeEscapeStats":    FunctionProductAggregate,
	"ResolvedTypeNames":       FunctionProductRebind,
	"ContextualTypeBindings":  FunctionProductRebind,
	"ResolvedValueNames":      FunctionProductRebind,
	"DenseNodeKeys":           FunctionProductRebind,
	"NodeTables":              FunctionProductRebind,
	"PackedLowering":          FunctionProductRebind,
	"ParallelFor":             FunctionProductRebind,
	"TaskGroupSubmits":        FunctionProductRebind,
	"CallArgDisjoint":         FunctionProductAggregate,
	"ReassignReclaims":        FunctionProductAggregate,
	"FuncDisjointParams":      FunctionProductAggregate,
	"LawIsCalls":              FunctionProductRebind,
	"LemmaCalls":              FunctionProductRebind,
	"GhostDecls":              FunctionProductRebind,
	"GhostContracts":          FunctionProductRebind,
	"RefinementChecks":        FunctionProductRebind,
	"CallArgRefinementChecks": FunctionProductRebind,
	"ReturnRefinementChecks":  FunctionProductRebind,
	"IndexBoundsProven":       FunctionProductRebind,
	// Contributions, origins and replaceable producer lifetimes must hydrate
	// alongside the public slice; a flat report overlay is not a full product.
	"ProofReport":      FunctionProductRebind | FunctionProductAnalyzerState,
	"ElisionSummary":   FunctionProductAggregate,
	"RequiresReport":   FunctionProductAggregate,
	"Defer":            FunctionProductRebind,
	"Fold":             FunctionProductRebind,
	"Lambdas":          FunctionProductRebind,
	"FunctionAnalyses": FunctionProductRebind,
	// Body seeds must be replayed, then BlockingPath and SCC Recursion results
	// cleared/recomputed; final summaries cannot simply be appended again.
	"ProgressSummaries":  FunctionProductRebind | FunctionProductAggregate,
	"AnnotatedFuncs":     FunctionProductGlobal,
	"LawFuzzObligations": FunctionProductAggregate,
	"ExportedTypes":      FunctionProductGlobal,
	"ExportedFuncs":      FunctionProductGlobal,
	"ExportedGlobals":    FunctionProductGlobal,
	"EASMModules":        FunctionProductGlobal,
	"Diagnostics":        FunctionProductRebind,
	// The pointer is invocation-local, but backend APIs delegate to live
	// analyzer facts and can trigger specialization; those facts need hydration.
	"analyzer": FunctionProductInvocation | FunctionProductAnalyzerState,
	// Set only while concurrent code generators share the result; nil otherwise.
	"shared": FunctionProductInvocation,
	// Private sparse-codegen views exist only during specialization publication.
	// Canonical retained products require these fields to stay zero-valued.
	"specializationRoot":       FunctionProductInvocation,
	"specializationCanonical":  FunctionProductInvocation,
	"specializationCurrent":    FunctionProductInvocation,
	"specializationNodes":      FunctionProductInvocation,
	"specializationOwned":      FunctionProductInvocation,
	"specializationProofMask":  FunctionProductInvocation,
	"specializationSpanProofs": FunctionProductInvocation,
	"specializationQueries":    FunctionProductInvocation,
}

func validateFunctionProductFields(typ reflect.Type, policies map[string]FunctionProductDisposition) error {
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		policy, ok := policies[name]
		if !ok || policy == 0 || policy & ^functionProductAllDispositions != 0 {
			return fmt.Errorf("unclassified semantic result field %s", name)
		}
		seen[name] = true
	}
	for name := range policies {
		if !seen[name] {
			return fmt.Errorf("obsolete function product policy %s", name)
		}
	}
	return nil
}

func FunctionProductRequirements() ([]FunctionProductRequirement, error) {
	typ := reflect.TypeOf(Result{})
	if err := validateFunctionProductFields(typ, functionProductFieldPolicies); err != nil {
		return nil, err
	}
	requirements := make([]FunctionProductRequirement, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		requirements = append(requirements, FunctionProductRequirement{name, functionProductFieldPolicies[name]})
	}
	return requirements, nil
}
