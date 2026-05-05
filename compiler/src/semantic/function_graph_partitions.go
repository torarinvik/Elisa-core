package semantic

import (
	"elisacore/src/ast"
	"sort"
	"strconv"
)

func ComputeGraphPartitions(fn *ast.FuncDecl) *GraphPartitions {
	cfg := ConstructCFG(fn)
	populateBasicFlowInstrs(cfg)
	return computeGraphPartitionsFromCFG(cfg)
}

func CheckBorrowedLocations(partitions *GraphPartitions, borrowed []string) bool {
	if partitions == nil {
		return true
	}
	for _, location := range borrowed {
		if partitions.ClassMutated(location) {
			return false
		}
	}
	return true
}

func (p *GraphPartitions) ClassOf(location string) (int, bool) {
	if p == nil || location == "" || p.locationIndex == nil {
		return 0, false
	}
	index, ok := p.locationIndex[location]
	if !ok {
		return 0, false
	}
	return p.find(index), true
}

func (p *GraphPartitions) SameClass(left string, right string) bool {
	leftClass, ok := p.ClassOf(left)
	if !ok {
		return false
	}
	rightClass, ok := p.ClassOf(right)
	if !ok {
		return false
	}
	return leftClass == rightClass
}

func (p *GraphPartitions) ClassMutated(location string) bool {
	if p == nil {
		return false
	}
	classID, ok := p.ClassOf(location)
	if !ok {
		return false
	}
	return p.mutated[classID]
}

func (p *GraphPartitions) IsMutated(location string) bool {
	return p.ClassMutated(location)
}

func (p *GraphPartitions) AliasSets() []FactAliasSet {
	if p == nil || len(p.locations) == 0 {
		return nil
	}
	groups := map[int][]string{}
	mutated := map[int]bool{}
	for index, location := range p.locations {
		root := p.find(index)
		groups[root] = append(groups[root], location)
	}
	for root := range groups {
		if p.mutated[root] {
			mutated[root] = true
		}
	}
	type aliasGroup struct {
		members []string
		mutated bool
	}
	ordered := make([]aliasGroup, 0, len(groups))
	for root, members := range groups {
		members = canonicalStringList(members)
		if len(members) <= 1 && !mutated[root] {
			continue
		}
		ordered = append(ordered, aliasGroup{members: members, mutated: mutated[root]})
	}
	sort.Slice(ordered, func(i, j int) bool {
		left := ""
		right := ""
		if len(ordered[i].members) != 0 {
			left = ordered[i].members[0]
		}
		if len(ordered[j].members) != 0 {
			right = ordered[j].members[0]
		}
		return left < right
	})
	out := make([]FactAliasSet, 0, len(ordered))
	for i, group := range ordered {
		out = append(out, FactAliasSet{ID: "alias-class#" + strconv.Itoa(i), Members: group.members, AffectedPaths: group.members, Mutated: group.mutated})
	}
	return out
}

func computeGraphPartitionsFromCFG(cfg *CFG) *GraphPartitions {
	partitions := newGraphPartitions()
	if cfg == nil {
		return partitions
	}
	for _, param := range cfg.ParamLocations {
		partitions.ensure(param)
	}
	for _, block := range cfg.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Kind {
			case FlowInstrAlias:
				partitions.union(instr.Location, instr.Source)
			case FlowInstrMutate:
				partitions.markMutated(instr.Location)
			case FlowInstrConsume:
				partitions.ensure(instr.Location)
			}
		}
	}
	return partitions
}

func newGraphPartitions() *GraphPartitions {
	return &GraphPartitions{locationIndex: map[string]int{}, mutated: map[int]bool{}}
}

func (p *GraphPartitions) ensure(location string) int {
	if p == nil || location == "" {
		return -1
	}
	if index, ok := p.locationIndex[location]; ok {
		return index
	}
	index := len(p.parent)
	p.locationIndex[location] = index
	p.locations = append(p.locations, location)
	p.parent = append(p.parent, index)
	p.rank = append(p.rank, 0)
	return index
}

func (p *GraphPartitions) find(index int) int {
	if p == nil || index < 0 || index >= len(p.parent) {
		return index
	}
	if p.parent[index] != index {
		p.parent[index] = p.find(p.parent[index])
	}
	return p.parent[index]
}

func (p *GraphPartitions) union(left string, right string) {
	if p == nil || left == "" || right == "" {
		return
	}
	leftIndex := p.ensure(left)
	rightIndex := p.ensure(right)
	if leftIndex < 0 || rightIndex < 0 {
		return
	}
	leftRoot := p.find(leftIndex)
	rightRoot := p.find(rightIndex)
	if leftRoot == rightRoot {
		return
	}
	if p.rank[leftRoot] < p.rank[rightRoot] {
		leftRoot, rightRoot = rightRoot, leftRoot
	}
	p.parent[rightRoot] = leftRoot
	if p.rank[leftRoot] == p.rank[rightRoot] {
		p.rank[leftRoot]++
	}
	if p.mutated[rightRoot] {
		p.mutated[leftRoot] = true
		delete(p.mutated, rightRoot)
	}
}

func (p *GraphPartitions) markMutated(location string) {
	if p == nil || location == "" {
		return
	}
	index := p.ensure(location)
	if index < 0 {
		return
	}
	p.mutated[p.find(index)] = true
}

func inferSinkParamsFromCFG(cfg *CFG) []bool {
	if cfg == nil || len(cfg.ParamLocations) == 0 {
		return nil
	}
	indexByParam := map[string]int{}
	for i, name := range cfg.ParamLocations {
		indexByParam[name] = i
	}
	blockCount := len(cfg.Blocks)
	if blockCount == 0 {
		return make([]bool, len(cfg.ParamLocations))
	}
	preds := make([][]int, blockCount)
	gen := make([][]bool, blockCount)
	for i, block := range cfg.Blocks {
		gen[i] = make([]bool, len(cfg.ParamLocations))
		for _, instr := range block.Instrs {
			if instr.Kind != FlowInstrConsume {
				continue
			}
			if index, ok := indexByParam[instr.Location]; ok {
				gen[i][index] = true
			}
		}
		for _, edge := range block.Edges {
			if edge.To >= 0 && edge.To < blockCount {
				preds[edge.To] = append(preds[edge.To], i)
			}
		}
	}
	in := make([][]bool, blockCount)
	out := make([][]bool, blockCount)
	for i := 0; i < blockCount; i++ {
		in[i] = make([]bool, len(cfg.ParamLocations))
		out[i] = make([]bool, len(cfg.ParamLocations))
		if i != cfg.Entry {
			for j := range in[i] {
				in[i][j] = true
			}
		}
	}
	changed := true
	for changed {
		changed = false
		for i := 0; i < blockCount; i++ {
			if i != cfg.Entry {
				nextIn := intersectPredOut(preds[i], out, len(cfg.ParamLocations))
				if !boolSliceEqual(in[i], nextIn) {
					copy(in[i], nextIn)
					changed = true
				}
			}
			nextOut := mergeMustConsume(in[i], gen[i])
			if !boolSliceEqual(out[i], nextOut) {
				copy(out[i], nextOut)
				changed = true
			}
		}
	}
	if len(cfg.ExitBlocks) == 0 {
		return make([]bool, len(cfg.ParamLocations))
	}
	mustConsume := make([]bool, len(cfg.ParamLocations))
	for i := range mustConsume {
		mustConsume[i] = true
	}
	for _, exit := range cfg.ExitBlocks {
		if exit < 0 || exit >= len(out) {
			continue
		}
		for i := range mustConsume {
			mustConsume[i] = mustConsume[i] && out[exit][i]
		}
	}
	return mustConsume
}

func intersectPredOut(preds []int, out [][]bool, size int) []bool {
	result := make([]bool, size)
	if len(preds) == 0 {
		return result
	}
	for i := 0; i < size; i++ {
		result[i] = true
	}
	for _, pred := range preds {
		if pred < 0 || pred >= len(out) {
			continue
		}
		for i := 0; i < size; i++ {
			result[i] = result[i] && out[pred][i]
		}
	}
	return result
}

func mergeMustConsume(base []bool, gen []bool) []bool {
	out := append([]bool(nil), base...)
	for i, value := range gen {
		out[i] = out[i] || value
	}
	return out
}

func boolSliceEqual(left []bool, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
