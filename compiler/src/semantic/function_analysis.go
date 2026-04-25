package semantic

import (
	"sort"
	"strconv"
	"strings"

	"llcontext/src/ast"
)

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
		out = append(out, FactAliasSet{ID: "alias-class#" + strconv.Itoa(i), Members: group.members, Mutated: group.mutated})
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

func (a *Analyzer) inferFuncSinkParamsForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.SinkParamsKnown || expr == nil {
		return
	}
	decl, resolvedType, ok := a.resolveSinkFuncDecl(expr)
	if !ok {
		if len(fnType.SinkParams) != len(fnType.Params) {
			fnType.SinkParams = make([]bool, len(fnType.Params))
		}
		fnType.SinkParamsKnown = true
		return
	}
	if resolvedType != nil && resolvedType != fnType {
		fnType = resolvedType
	}
	a.inferFuncSinkParams(decl, fnType)
}

func (a *Analyzer) inferFuncSinkParams(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.SinkParamsKnown {
		return
	}
	if a.sinkParamInferenceInProgress[fn] {
		return
	}
	a.sinkParamInferenceInProgress[fn] = true
	defer delete(a.sinkParamInferenceInProgress, fn)

	cfg := a.constructCFG(fn)
	configureCFGParamLocations(cfg, fnType)
	populateBasicFlowInstrs(cfg)
	a.addImplicitSinkFlowInstrs(cfg)
	fnType.SinkParams = inferSinkParamsFromCFG(cfg)
	fnType.SinkParamsKnown = true
	analysis := a.functionAnalyses[fn]
	if analysis == nil {
		analysis = &FunctionAnalysis{}
	}
	analysis.CFG = cfg
	analysis.SinkParams = append([]bool(nil), fnType.SinkParams...)
	a.functionAnalyses[fn] = analysis
}

func (a *Analyzer) addImplicitSinkFlowInstrs(cfg *CFG) {
	if a == nil || cfg == nil {
		return
	}
	for i := range cfg.Blocks {
		block := &cfg.Blocks[i]
		for _, node := range block.Nodes {
			a.appendImplicitSinkFlowInstrsForNode(block, node)
		}
	}
}

func (a *Analyzer) appendImplicitSinkFlowInstrsForNode(block *CFGBlock, node ast.Node) {
	if a == nil || block == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.VarDeclStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.TupleBindStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AsRefAssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AugAssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.ReturnStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.ExprStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Expr)
	case *ast.RegionStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Capacity)
	case *ast.PoolStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Workers)
	case *ast.LockStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Mutex)
	case *ast.OpenStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.ViewStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.MoveBindStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.LetDestructureStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.InStoreStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.ForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Step)
	case *ast.IterForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Source)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Filter)
	case *ast.ParallelForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Source)
	case *ast.IfStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
	case *ast.WhileStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
	case *ast.MatchStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	}
}

func (a *Analyzer) appendImplicitSinkFlowInstrsForExpr(block *CFGBlock, expr ast.Expr) {
	if a == nil || block == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Inner)
	case *ast.CastExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.CanExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Expr)
	case *ast.MoveExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.AddrOfExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.BinaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Left)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Right)
	case *ast.UnaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.CallExpr:
		a.appendImplicitSinkCallInstrs(block, n)
	case *ast.FieldExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
	case *ast.IndexExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Index)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.SliceExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.appendImplicitSinkFlowInstrsForExpr(block, arg)
		}
	case *ast.RecordUpdateExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Base)
		for _, arg := range n.Args {
			a.appendImplicitSinkFlowInstrsForExpr(block, arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.appendImplicitSinkFlowInstrsForExpr(block, elem)
		}
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.appendImplicitSinkFlowInstrsForExpr(block, elem)
		}
	case *ast.TryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.UnwrapElseExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.OptionalBindExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.TernaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Alt)
	case *ast.MatchExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.VisitExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		for _, arm := range n.Arms {
			a.appendImplicitSinkFlowInstrsForExpr(block, arm.Guard)
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		for _, arm := range n.Arms {
			a.appendImplicitSinkFlowInstrsForExpr(block, arm.Guard)
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.appendImplicitSinkFlowInstrsForExpr(block, target)
		}
	}
}

func (a *Analyzer) appendImplicitSinkCallInstrs(block *CFGBlock, call *ast.CallExpr) {
	if a == nil || block == nil || call == nil {
		return
	}
	fnType, decl, ok := a.resolveSinkFuncType(call.Func)
	if ok && fnType != nil {
		if !fnType.SinkParamsKnown {
			if decl != nil {
				a.inferFuncSinkParams(decl, fnType)
			} else {
				fnType.SinkParams = make([]bool, len(fnType.Params))
				fnType.SinkParamsKnown = true
			}
		}
		for i, arg := range call.Args {
			if i >= len(fnType.SinkParams) || !fnType.SinkParams[i] {
				continue
			}
			if _, moved := explicitMoveOperand(arg); moved {
				continue
			}
			if location := flowLocationForExpr(arg); location != "" {
				appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: location, Position: call.Pos(), Note: "sink arg to " + fnType.Name})
			}
		}
	}
	a.appendImplicitSinkFlowInstrsForExpr(block, call.Func)
	for _, arg := range call.Args {
		a.appendImplicitSinkFlowInstrsForExpr(block, arg)
	}
}

func (a *Analyzer) resolveSinkFuncDecl(expr ast.Expr) (*ast.FuncDecl, *FuncType, bool) {
	if a == nil || expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.resolveSinkFuncDecl(n.Inner)
	case *ast.SpecializeExpr:
		return a.resolveSinkFuncDecl(n.Operand)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				fnType, _ := sym.Type.(*FuncType)
				if decl, ok := sym.Node.(*ast.FuncDecl); ok {
					return decl, fnType, true
				}
				if fnType != nil {
					return nil, fnType, true
				}
			}
		}
		if a.globalScope != nil {
			if sym, ok := a.globalScope.Lookup(n.Name); ok {
				fnType, _ := sym.Type.(*FuncType)
				if decl, ok := sym.Node.(*ast.FuncDecl); ok {
					return decl, fnType, true
				}
				if fnType != nil {
					return nil, fnType, true
				}
			}
		}
	}
	if fnType, ok := a.exprTypes[expr].(*FuncType); ok {
		return nil, fnType, true
	}
	return nil, nil, false
}

func (a *Analyzer) resolveSinkFuncType(expr ast.Expr) (*FuncType, *ast.FuncDecl, bool) {
	decl, fnType, ok := a.resolveSinkFuncDecl(expr)
	return fnType, decl, ok
}

func (a *Analyzer) constructCFG(fn *ast.FuncDecl) *CFG {
	if a == nil {
		return ConstructCFG(fn)
	}
	return constructCFG(fn, a.guardFactsForConditionWithMetadata)
}

func (a *Analyzer) finalizeFunctionAnalysis(fn *ast.FuncDecl, fnType *FuncType) {
	if a == nil || fn == nil || fnType == nil {
		return
	}
	if !fnType.SinkParamsKnown {
		a.inferFuncSinkParams(fn, fnType)
	}
	cfg := a.constructCFG(fn)
	configureCFGParamLocations(cfg, fnType)
	populateBasicFlowInstrs(cfg)
	a.addImplicitSinkFlowInstrs(cfg)
	partitions := computeGraphPartitionsFromCFG(cfg)
	cleanupPlan := SynthesizeParamCleanupPlan(fn, fnType)
	returnIsolation := summarizeReturnIsolation(fn, fnType, partitions)
	fnType.ReturnIsolation = returnIsolation
	fnType.ReturnIsolationKnown = true
	cfgTransforms := populateCFGBlockFactTransforms(cfg)
	factTransforms := a.currentConservativeCallWideningTransforms()
	factTransforms = append(factTransforms, cfgTransforms...)
	factTransforms = append(factTransforms, a.currentRegionFactTransforms...)
	factTransforms = append(factTransforms, factTransformsFromPoststates(fnType)...)
	factTransforms = append(factTransforms, factTransformsFromPermissions(fnType)...)
	aliasSets := partitions.AliasSets()
	factTransforms = append(factTransforms, factTransformsFromAliasSets(aliasSets)...)
	factTransforms = dedupeAndSortFactTransforms(factTransforms)
	blockFactTransforms := collectCFGBlockFactTransforms(cfg)
	factSnapshot := buildFunctionFactSnapshot(fnType, cfg, factTransforms, aliasSets)
	factExitSummary := buildFunctionFactExitSummary(factTransforms)
	analysis := &FunctionAnalysis{
		CFG:                 cfg,
		Partitions:          partitions,
		CleanupPlan:         cleanupPlan,
		SinkParams:          append([]bool(nil), fnType.SinkParams...),
		ReturnIsolation:     returnIsolation,
		FactTransforms:      factTransforms,
		BlockFactTransforms: blockFactTransforms,
		FactSnapshot:        factSnapshot,
		FactExitSummary:     factExitSummary,
		AliasSets:           aliasSets,
	}
	a.functionAnalyses[fn] = analysis
}

func collectCFGBlockFactTransforms(cfg *CFG) []FactBlockTransforms {
	if cfg == nil {
		return nil
	}
	out := make([]FactBlockTransforms, 0, len(cfg.Blocks))
	for _, block := range cfg.Blocks {
		if len(block.FactTransforms) == 0 {
			continue
		}
		out = append(out, FactBlockTransforms{BlockID: block.ID, Transforms: append([]FactTransform(nil), block.FactTransforms...)})
	}
	return out
}

func buildFunctionFactSnapshot(fnType *FuncType, cfg *CFG, transforms []FactTransform, aliasSets []FactAliasSet) FactSnapshot {
	snapshot := FactSnapshot{}
	if cfg != nil {
		snapshot.Parameters = append(snapshot.Parameters, cfg.ParamLocations...)
	}
	if fnType != nil && fnType.Return != nil && !isVoidType(fnType.Return) {
		snapshot.Returns = append(snapshot.Returns, fnType.Return.String())
		snapshot.StoreDeps = append(snapshot.StoreDeps, storeDependencyFactLabelsFromRegionRefState(fnType.ReturnProvenance)...)
	}
	for _, transform := range transforms {
		if path := factPathFromTarget(transform.Target); path.Target != "" {
			snapshot.PathFacts = append(snapshot.PathFacts, path)
		}
		switch transform.Kind {
		case FactTransformRequire:
			snapshot.RequiredEffects = append(snapshot.RequiredEffects, transform.Target)
		case FactTransformEnsure:
			snapshot.Ensured = append(snapshot.Ensured, transform.Target)
		case FactTransformRefine:
			snapshot.Refined = append(snapshot.Refined, transform.Target)
		case FactTransformWiden:
			snapshot.Widened = append(snapshot.Widened, transform.Target)
		case FactTransformConsume:
			snapshot.Consumed = append(snapshot.Consumed, transform.Target)
		case FactTransformProduce:
			if hasFactClass(transform.Classes, FactErrorPath) {
				snapshot.ErrorExits = append(snapshot.ErrorExits, transform.Reason)
			} else if transform.Target == "<return>" {
				snapshot.Returns = append(snapshot.Returns, transform.Source)
			} else {
				snapshot.Produced = append(snapshot.Produced, transform.Target)
				if hasFactClass(transform.Classes, FactStoreDeps) {
					if deps := factTransformDetailValue(transform.Details, "store_deps"); deps != "" {
						snapshot.HandleStoreDeps = append(snapshot.HandleStoreDeps, transform.Target+"<-"+deps)
					}
				}
			}
		case FactTransformInvalidate:
			snapshot.InvalidatedRegions = append(snapshot.InvalidatedRegions, transform.Target)
		case FactTransformRebase:
			snapshot.RebasedStores = append(snapshot.RebasedStores, transform.Target)
		}
	}
	for _, set := range aliasSets {
		if set.ID != "" {
			snapshot.AliasClasses = append(snapshot.AliasClasses, set.ID)
		}
	}
	snapshot.Parameters = canonicalStringList(snapshot.Parameters)
	snapshot.Returns = canonicalStringList(snapshot.Returns)
	snapshot.Consumed = canonicalStringList(snapshot.Consumed)
	snapshot.Produced = canonicalStringList(snapshot.Produced)
	snapshot.InvalidatedRegions = canonicalStringList(snapshot.InvalidatedRegions)
	snapshot.RebasedStores = canonicalStringList(snapshot.RebasedStores)
	snapshot.RequiredEffects = canonicalStringList(snapshot.RequiredEffects)
	snapshot.Ensured = canonicalStringList(snapshot.Ensured)
	snapshot.Refined = canonicalStringList(snapshot.Refined)
	snapshot.Widened = canonicalStringList(snapshot.Widened)
	snapshot.ErrorExits = canonicalStringList(snapshot.ErrorExits)
	snapshot.StoreDeps = canonicalStringList(snapshot.StoreDeps)
	snapshot.PathFacts = canonicalFactPaths(snapshot.PathFacts)
	snapshot.AliasClasses = canonicalStringList(snapshot.AliasClasses)
	snapshot.HandleStoreDeps = canonicalStringList(snapshot.HandleStoreDeps)
	return snapshot
}

func buildFunctionFactExitSummary(transforms []FactTransform) FactExitSummary {
	summary := FactExitSummary{}
	for _, transform := range transforms {
		text := FormatFactTransform(transform)
		if text == "" {
			continue
		}
		if hasFactClass(transform.Classes, FactErrorPath) {
			summary.Error = append(summary.Error, text)
			continue
		}
		if transform.Kind == FactTransformEnsure || (transform.Kind == FactTransformProduce && transform.Target == "<return>") {
			summary.Normal = append(summary.Normal, text)
		}
	}
	summary.Normal = canonicalStringList(summary.Normal)
	summary.Error = canonicalStringList(summary.Error)
	return summary
}

func factPathFromTarget(target string) FactPath {
	if target == "" || strings.HasPrefix(target, "<") {
		return FactPath{}
	}
	root := flowLocationRoot(target)
	if root == "" {
		return FactPath{}
	}
	path := strings.TrimPrefix(target[len(root):], ".")
	return FactPath{Target: target, Root: root, Path: path}
}

func factTransformsFromAliasSets(sets []FactAliasSet) []FactTransform {
	if len(sets) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0, len(sets)*2)
	for _, set := range sets {
		members := canonicalStringList(set.Members)
		if set.ID == "" || len(members) == 0 {
			continue
		}
		details := []FactTransformDetail{{Name: "members", Value: strings.Join(members, "|")}}
		transforms = append(transforms, FactTransform{Kind: FactTransformRefine, Classes: []FactClass{FactAliasClass}, Target: set.ID, Source: strings.Join(members, ","), SourceKind: FactSourceFlowInstr, Details: details, Reason: "alias equivalence set"})
		if set.Mutated {
			transforms = append(transforms, FactTransform{Kind: FactTransformRecompute, Classes: []FactClass{FactAliasClass}, Target: set.ID, Source: strings.Join(members, ","), SourceKind: FactSourceFlowInstr, Details: details, Reason: "mutation recomputes facts for alias class"})
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func hasFactClass(classes []FactClass, target FactClass) bool {
	for _, class := range classes {
		if class == target {
			return true
		}
	}
	return false
}

func storeDependencyFactLabelsFromRegionRefState(state regionRefState) []string {
	out := make([]string, 0)
	storeDependencyFactLabelsFromRegionRefStateSeen(state, map[uintptr]bool{}, &out)
	return canonicalStringList(out)
}

func storeDependencyFactLabelsFromRegionRefStateSeen(state regionRefState, seen map[uintptr]bool, out *[]string) {
	for _, dep := range state.StoreDeps {
		if dep.Type != nil {
			*out = append(*out, dep.Type.String())
		}
	}
	fieldsID := regionRefFieldsIdentity(state.Fields)
	if fieldsID != 0 {
		if seen[fieldsID] {
			return
		}
		seen[fieldsID] = true
		defer delete(seen, fieldsID)
	}
	for _, field := range state.Fields {
		storeDependencyFactLabelsFromRegionRefStateSeen(field, seen, out)
	}
}

func configureCFGParamLocations(cfg *CFG, fnType *FuncType) {
	if cfg == nil || fnType == nil {
		return
	}
	cfg.ParamLocations = cfg.ParamLocations[:0]
	for i := range fnType.Params {
		name := functionParamName(fnType, i)
		if name == "" {
			name = "<param>"
		}
		cfg.ParamLocations = append(cfg.ParamLocations, name)
	}
}

func summarizeReturnIsolation(fn *ast.FuncDecl, fnType *FuncType, partitions *GraphPartitions) ReturnIsolationSummary {
	summary := ReturnIsolationSummary{Known: true}
	if fn == nil || fnType == nil {
		return summary
	}
	aliasLocations := map[string]bool{}
	forEachRegionParamDep(fnType.ReturnProvenance, func(index int) {
		if name := functionParamName(fnType, index); name != "" {
			aliasLocations[name] = true
		}
	})
	collectBorrowedOwnerAliasLocations(fnType, fnType.ReturnBorrowedOwnerRefs, aliasLocations)
	if len(aliasLocations) == 0 {
		summary.Isolated = true
		return summary
	}
	summary.Isolated = false
	paramAliases := map[int]bool{}
	mutableAliases := map[int]bool{}
	for location := range aliasLocations {
		summary.AliasLocations = append(summary.AliasLocations, location)
		root := flowLocationRoot(location)
		index := functionParamIndex(fnType, root)
		if index >= 0 {
			paramAliases[index] = true
			if partitions != nil && partitions.ClassMutated(location) {
				mutableAliases[index] = true
			}
		}
	}
	sort.Strings(summary.AliasLocations)
	for index := range paramAliases {
		summary.AliasParamIndices = append(summary.AliasParamIndices, index)
	}
	for index := range mutableAliases {
		summary.AliasMutableParamIndices = append(summary.AliasMutableParamIndices, index)
	}
	sort.Ints(summary.AliasParamIndices)
	sort.Ints(summary.AliasMutableParamIndices)
	return summary
}

func collectBorrowedOwnerAliasLocations(fnType *FuncType, summary borrowedOwnerRefSummary, out map[string]bool) {
	if fnType == nil || out == nil {
		return
	}
	if summary.HasDirect {
		if location := borrowedOwnerAliasLocation(fnType, summary.Direct); location != "" {
			out[location] = true
		}
	}
	for _, child := range summary.Fields {
		collectBorrowedOwnerAliasLocations(fnType, child, out)
	}
}

func borrowedOwnerAliasLocation(fnType *FuncType, target borrowedOwnerRefSummaryTarget) string {
	base := functionParamName(fnType, target.ParamIndex)
	if base == "" {
		return ""
	}
	return base + borrowAnnotationPathSuffix(target.Path)
}

func borrowAnnotationPathSuffix(path []borrowReturnAnnotationStep) string {
	if len(path) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, step := range path {
		switch {
		case step.Field != "":
			builder.WriteByte('.')
			builder.WriteString(step.Field)
		case step.Wildcard:
			builder.WriteString("[*]")
		case step.Index != nil:
			builder.WriteString("[")
			builder.WriteString(strconvFormatInt(*step.Index))
			builder.WriteString("]")
		}
	}
	return builder.String()
}

func functionParamName(fnType *FuncType, index int) string {
	if fnType == nil || index < 0 || index >= len(fnType.Params) {
		return ""
	}
	if index < len(fnType.ExplicitParamNames) {
		return fnType.ExplicitParamNames[index]
	}
	implicitIndex := index - len(fnType.ExplicitParamNames)
	if implicitIndex >= 0 && implicitIndex < len(fnType.ImplicitParamNames) {
		return fnType.ImplicitParamNames[implicitIndex]
	}
	return ""
}

func functionParamIndex(fnType *FuncType, name string) int {
	if fnType == nil || name == "" {
		return -1
	}
	for i, paramName := range fnType.ExplicitParamNames {
		if paramName == name {
			return i
		}
	}
	for i, paramName := range fnType.ImplicitParamNames {
		if paramName == name {
			return len(fnType.ExplicitParamNames) + i
		}
	}
	return -1
}

func dedupeStringSlice(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func returnIsolationSummariesEqual(left ReturnIsolationSummary, right ReturnIsolationSummary) bool {
	if left.Known != right.Known || left.Isolated != right.Isolated {
		return false
	}
	if len(left.AliasLocations) != len(right.AliasLocations) || len(left.AliasParamIndices) != len(right.AliasParamIndices) || len(left.AliasMutableParamIndices) != len(right.AliasMutableParamIndices) {
		return false
	}
	for i := range left.AliasLocations {
		if left.AliasLocations[i] != right.AliasLocations[i] {
			return false
		}
	}
	for i := range left.AliasParamIndices {
		if left.AliasParamIndices[i] != right.AliasParamIndices[i] {
			return false
		}
	}
	for i := range left.AliasMutableParamIndices {
		if left.AliasMutableParamIndices[i] != right.AliasMutableParamIndices[i] {
			return false
		}
	}
	return true
}

func strconvFormatInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + (value % 10))
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
