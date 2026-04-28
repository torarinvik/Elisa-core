package semantic

type ReturnIsolationSummary struct {
	Known                    bool
	Isolated                 bool
	AliasLocations           []string
	AliasParamIndices        []int
	AliasMutableParamIndices []int
}

type FunctionAnalysis struct {
	CFG                 *CFG
	Partitions          *GraphPartitions
	CleanupPlan         CleanupPlan
	SinkParams          []bool
	ReturnIsolation     ReturnIsolationSummary
	FactTransforms      []FactTransform
	BlockFactTransforms []FactBlockTransforms
	FactSnapshot        FactSnapshot
	FactExitSummary     FactExitSummary
	AliasSets           []FactAliasSet
	EffectSummary       FactEffectSummary
}

type FactBlockTransforms struct {
	BlockID    int
	Transforms []FactTransform
}

type GraphPartitions struct {
	parent        []int
	rank          []int
	locations     []string
	locationIndex map[string]int
	mutated       map[int]bool
}

func (s ReturnIsolationSummary) CanAlias(paramIndex int) bool {
	for _, index := range s.AliasParamIndices {
		if index == paramIndex {
			return true
		}
	}
	return false
}

func (s ReturnIsolationSummary) AliasesMutableParam(paramIndex int) bool {
	for _, index := range s.AliasMutableParamIndices {
		if index == paramIndex {
			return true
		}
	}
	return false
}

func (s ReturnIsolationSummary) CanAliasLocation(location string) bool {
	for _, candidate := range s.AliasLocations {
		if candidate == location {
			return true
		}
	}
	return false
}
