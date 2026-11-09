"""Reviewed responsibility groups for generated specialization fact accessors."""

FIELD_GROUPS = {
    "expression_facts": {"ExprTypes", "RewriteDefaults", "OptionalBindSourceTypes", "ExprFacts", "CastHooks"},
    "call_resolution": {"InterfaceMethodRefs", "SafeCalls", "InitCalls", "PostfixShorthandCalls"},
    "type_and_region": {"PermGrowthOps", "RegionStacks", "ResolvedTypeNames", "ContextualTypeBindings", "ResolvedValueNames"},
    "parallel_and_node_tables": {"DenseNodeKeys", "NodeTables", "ParallelFor", "TaskGroupSubmits", "CallArgDisjoint"},
    "laws_and_ghosts": {"LawIsCalls", "LemmaCalls", "GhostDecls", "GhostContracts"},
    "refinement_and_bounds": {"RefinementChecks", "CallArgRefinementChecks", "ReturnRefinementChecks", "IndexBoundsProven"},
    "control_flow": {"ReassignReclaims", "FuncDisjointParams", "Defer", "Fold", "Lambdas"},
    "function_analysis": {"FunctionAnalyses", "ProgressSummaries"},
}
