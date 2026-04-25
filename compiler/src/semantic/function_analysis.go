package semantic

import (
	"sort"
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
	CFG             *CFG
	Partitions      *GraphPartitions
	CleanupPlan     CleanupPlan
	SinkParams      []bool
	ReturnIsolation ReturnIsolationSummary
	FactTransforms  []FactTransform
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

func populateBasicFlowInstrs(cfg *CFG) {
	if cfg == nil {
		return
	}
	for i := range cfg.Blocks {
		block := &cfg.Blocks[i]
		for _, node := range block.Nodes {
			appendBasicFlowInstrsForNode(block, node)
		}
	}
}

func appendBasicFlowInstrsForNode(block *CFGBlock, node ast.Node) {
	if block == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.VarDeclStmt:
		if loc := flowLocationForExpr(n.Value); loc != "" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrAlias, Location: n.Name, Source: loc, Note: "var init alias"})
		}
		appendProduceFlowInstrForExpr(block, n.Name, n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.TupleBindStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.LetDestructureStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AsRefAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign-as-ref")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AugAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "aug-assign")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.ReturnStmt:
		appendProduceFlowInstrForExpr(block, "<return>", n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrReturn, Note: "return"})
	case *ast.ExprStmt:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.RegionStmt:
		appendBasicFlowExprInstrs(block, n.Capacity)
	case *ast.MarkStmt:
	case *ast.RestoreStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.RegionName, Source: n.MarkName, Note: "restore region checkpoint"})
	case *ast.ResetStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Note: "reset region"})
	case *ast.DestroyStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Note: "destroy region"})
	case *ast.PoolStmt:
		appendBasicFlowExprInstrs(block, n.Workers)
	case *ast.LockStmt:
		appendBasicFlowExprInstrs(block, n.Mutex)
	case *ast.OpenStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.ViewStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				appendBasicFlowExprInstrs(block, arg.Value)
			}
		}
	case *ast.MoveBindStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.InStoreStmt:
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.CanStmt:
	case *ast.ForStmt:
		appendBasicFlowExprInstrs(block, n.Start)
		appendBasicFlowExprInstrs(block, n.End)
		appendBasicFlowExprInstrs(block, n.Step)
	case *ast.IterForStmt:
		appendBasicFlowExprInstrs(block, n.Source)
		appendBasicFlowExprInstrs(block, n.Filter)
	case *ast.ParallelForStmt:
		appendBasicFlowExprInstrs(block, n.Source)
	case *ast.IfStmt:
		appendBasicFlowExprInstrs(block, n.Cond)
	case *ast.WhileStmt:
		appendBasicFlowExprInstrs(block, n.Cond)
	case *ast.MatchStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	}
}

func appendBasicFlowExprInstrs(block *CFGBlock, expr ast.Expr) {
	if block == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		appendBasicFlowExprInstrs(block, n.Inner)
	case *ast.CastExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.CanExpr:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.MoveExpr:
		if loc := flowLocationForExpr(n.Operand); loc != "" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: loc, Note: "explicit move"})
		}
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.AllocExpr:
		appendBasicFlowExprInstrs(block, n.Owner)
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.NodeSpan)
	case *ast.AddrOfExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.BinaryExpr:
		appendBasicFlowExprInstrs(block, n.Left)
		appendBasicFlowExprInstrs(block, n.Right)
	case *ast.UnaryExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.CallExpr:
		appendRebaseFlowInstrForCall(block, n)
		appendBasicFlowExprInstrs(block, n.Func)
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.FieldExpr:
		appendBasicFlowExprInstrs(block, n.Object)
	case *ast.IndexExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Index)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.SliceExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Start)
		appendBasicFlowExprInstrs(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.RecordUpdateExpr:
		appendBasicFlowExprInstrs(block, n.Base)
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			appendBasicFlowExprInstrs(block, elem)
		}
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			appendBasicFlowExprInstrs(block, elem)
		}
	case *ast.TryExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.UnwrapElseExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.OptionalBindExpr:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.TernaryExpr:
		appendBasicFlowExprInstrs(block, n.Cond)
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Alt)
	case *ast.MatchExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.VisitExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		for _, arm := range n.Arms {
			appendBasicFlowExprInstrs(block, arm.Guard)
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		for _, arm := range n.Arms {
			appendBasicFlowExprInstrs(block, arm.Guard)
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			appendBasicFlowExprInstrs(block, target)
		}
	}
}

func appendProduceFlowInstrForExpr(block *CFGBlock, target string, expr ast.Expr) {
	if block == nil || target == "" || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		appendProduceFlowInstrForExpr(block, target, n.Inner)
	case *ast.CastExpr:
		appendProduceFlowInstrForExpr(block, target, n.Operand)
	case *ast.CanExpr:
		appendProduceFlowInstrForExpr(block, target, n.Expr)
	case *ast.AllocExpr:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Source: flowLocationForExpr(n.Owner), Note: allocProduceFlowNote(n)})
	case *ast.CallExpr:
		if callIdentName(n) == "freeze" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Note: "freeze produces frozen store"})
		}
	}
}

func appendRebaseFlowInstrForCall(block *CFGBlock, call *ast.CallExpr) {
	if block == nil || call == nil || callIdentName(call) != "freeze" || len(call.Args) != 1 {
		return
	}
	if target := flowLocationForExpr(call.Args[0]); target != "" {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrRebase, Location: target, Note: "freeze rebases store provenance"})
	}
}

func allocProduceFlowNote(expr *ast.AllocExpr) string {
	if expr != nil && expr.NodeSugar {
		return "node construction"
	}
	return "allocation produces value"
}

func appendMutationFlowInstr(block *CFGBlock, location string, note string) {
	if block == nil || location == "" {
		return
	}
	for _, candidate := range mutationLocationPrefixes(location) {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrMutate, Location: candidate, Note: note})
	}
}

func appendFlowInstrUnique(block *CFGBlock, instr FlowInstr) {
	if block == nil {
		return
	}
	for _, existing := range block.Instrs {
		if existing.Kind == instr.Kind && existing.Location == instr.Location && existing.Source == instr.Source && existing.Note == instr.Note {
			return
		}
	}
	block.Instrs = append(block.Instrs, instr)
}

func mutationLocationPrefixes(location string) []string {
	if location == "" {
		return nil
	}
	out := []string{location}
	root := flowLocationRoot(location)
	if root != "" && root != location {
		out = append(out, root)
	}
	return dedupeStringSlice(out)
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
				appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: location, Note: "sink arg to " + fnType.Name})
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
	factTransforms := a.currentConservativeCallWideningTransforms()
	factTransforms = append(factTransforms, factTransformsFromCFGGuards(cfg)...)
	factTransforms = append(factTransforms, factTransformsFromCFGFlowInstrs(cfg)...)
	factTransforms = append(factTransforms, factTransformsFromPoststates(fnType)...)
	factTransforms = append(factTransforms, factTransformsFromPermissions(fnType)...)
	factTransforms = dedupeAndSortFactTransforms(factTransforms)
	analysis := &FunctionAnalysis{
		CFG:             cfg,
		Partitions:      partitions,
		CleanupPlan:     cleanupPlan,
		SinkParams:      append([]bool(nil), fnType.SinkParams...),
		ReturnIsolation: returnIsolation,
		FactTransforms:  factTransforms,
	}
	a.functionAnalyses[fn] = analysis
}

func (a *Analyzer) currentConservativeCallWideningTransforms() []FactTransform {
	if a == nil || len(a.currentConservativeCallWidenings) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for root, paths := range a.currentConservativeCallWidenings {
		if root == nil {
			continue
		}
		for _, path := range paths {
			transforms = append(transforms, FactTransform{
				Kind:    FactTransformWiden,
				Classes: []FactClass{FactTypestate},
				Target:  namedStateTargetDisplayName(root, path),
				Reason:  "ref call without matching ensures",
			})
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromCFGGuards(cfg *CFG) []FactTransform {
	if cfg == nil {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for _, block := range cfg.Blocks {
		for _, edge := range block.Edges {
			transforms = append(transforms, factTransformsFromGuardFactSet(edge.Guard)...)
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromCFGFlowInstrs(cfg *CFG) []FactTransform {
	if cfg == nil {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for _, block := range cfg.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Kind {
			case FlowInstrAlias:
				if instr.Location == "" || instr.Source == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRefine,
					Classes: []FactClass{FactAliasClass},
					Target:  instr.Location,
					Source:  instr.Source,
					Reason:  flowInstrFactReason(instr, "alias fact"),
				})
			case FlowInstrInvalidate:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformInvalidate,
					Classes: []FactClass{FactRegionDeps},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "invalidate region dependencies"),
				})
			case FlowInstrProduce:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformProduce,
					Classes: []FactClass{FactRepresentation, FactStorage},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "produce value"),
				})
			case FlowInstrRebase:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRebase,
					Classes: []FactClass{FactStoreDeps},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "rebase provenance"),
				})
			case FlowInstrConsume:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformConsume,
					Classes: []FactClass{FactUsage},
					Target:  instr.Location,
					Source:  "control-flow instruction",
					Reason:  flowInstrFactReason(instr, "consume value"),
				})
			case FlowInstrMutate:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRecompute,
					Classes: []FactClass{FactTypestate},
					Target:  instr.Location,
					Source:  "control-flow instruction",
					Reason:  flowInstrFactReason(instr, "mutation recomputes derived facts"),
				})
			}
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func flowInstrFactSource(instr FlowInstr, fallback string) string {
	if instr.Source != "" {
		return instr.Source
	}
	return fallback
}

func flowInstrFactReason(instr FlowInstr, fallback string) string {
	if instr.Note != "" {
		return instr.Note
	}
	return fallback
}

func factTransformsFromPoststates(fnType *FuncType) []FactTransform {
	if fnType == nil || len(fnType.Poststates) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0, len(fnType.Poststates))
	for _, poststate := range fnType.Poststates {
		target := functionPoststateTargetName(fnType, poststate)
		if target == "" {
			continue
		}
		transform := FactTransform{
			Kind:   FactTransformEnsure,
			Target: target,
			Source: "ensures " + funcPoststateConditionLabel(poststate.Condition),
		}
		switch poststate.Kind {
		case FuncPoststateKindNamedState:
			transform.Classes = []FactClass{FactTypestate}
			transform.Reason = "ensures typestate " + strings.Join(poststate.StateCases, "|")
		case FuncPoststateKindRefState:
			transform.Classes = []FactClass{FactRefState}
			transform.Reason = "ensures refstate " + ast.RefStateMarker(ast.RefState(poststate.RefState))
		case FuncPoststateKindPreserve:
			transform.Classes = []FactClass{FactTypestate, FactRefState}
			transform.Reason = "ensures preserve"
		default:
			continue
		}
		transforms = append(transforms, transform)
	}
	return dedupeAndSortFactTransforms(transforms)
}

func functionPoststateTargetName(fnType *FuncType, poststate FuncPoststate) string {
	base := functionParamName(fnType, poststate.ParamIndex)
	if base == "" {
		return ""
	}
	return base + borrowAnnotationPathSuffix(poststate.Path)
}

func factTransformsFromPermissions(fnType *FuncType) []FactTransform {
	if fnType == nil {
		return nil
	}
	refs := functionPermissionRefs(fnType)
	if len(refs) == 0 {
		return nil
	}
	refs = canonicalizePermissionRefs(refs)
	transforms := make([]FactTransform, 0, len(refs))
	for _, ref := range refs {
		target := PermissionRefString(ref)
		if target == "" {
			continue
		}
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRequire,
			Classes: []FactClass{FactEffects},
			Target:  target,
			Source:  "function signature",
			Reason:  "requires effect authority",
		})
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromGuardFactSet(guards GuardFactSet) []FactTransform {
	transforms := make([]FactTransform, 0)
	for _, target := range sortedBoolFactKeys(guards.NonNull) {
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactRefState},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves non-null",
		})
	}
	for _, target := range sortedBoolFactKeys(guards.Null) {
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactRefState},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves null",
		})
	}
	variantTargets := make([]string, 0, len(guards.PackedVariants))
	for target := range guards.PackedVariants {
		variantTargets = append(variantTargets, target)
	}
	sort.Strings(variantTargets)
	for _, target := range variantTargets {
		guard := guards.PackedVariants[target]
		if guard.EnumName == "" || guard.VariantName == "" {
			continue
		}
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactTypestate},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves variant " + guard.EnumName + "." + guard.VariantName,
		})
	}
	lefts := make([]string, 0, len(guards.Leq))
	for left := range guards.Leq {
		lefts = append(lefts, left)
	}
	sort.Strings(lefts)
	for _, left := range lefts {
		rights := sortedBoolFactKeys(guards.Leq[left])
		for _, right := range rights {
			transforms = append(transforms, FactTransform{
				Kind:    FactTransformRefine,
				Classes: []FactClass{FactOptimization},
				Target:  left,
				Source:  "control-flow guard",
				Reason:  "guard proves <= " + right,
			})
		}
	}
	return transforms
}

func sortedBoolFactKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key != "" && value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func dedupeAndSortFactTransforms(transforms []FactTransform) []FactTransform {
	if len(transforms) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]FactTransform, 0, len(transforms))
	for _, transform := range transforms {
		if transform.Kind == "" {
			continue
		}
		key := factTransformDedupeKey(transform)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, transform)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		if factClassListKey(out[i].Classes) != factClassListKey(out[j].Classes) {
			return factClassListKey(out[i].Classes) < factClassListKey(out[j].Classes)
		}
		if out[i].Reason != out[j].Reason {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func factTransformDedupeKey(transform FactTransform) string {
	return strings.Join([]string{
		transform.Kind.String(),
		transform.Target,
		factClassListKey(transform.Classes),
		transform.Source,
		transform.Reason,
	}, "\x00")
}

func factClassListKey(classes []FactClass) string {
	if len(classes) == 0 {
		return ""
	}
	values := make([]string, 0, len(classes))
	for _, class := range classes {
		values = append(values, class.String())
	}
	return strings.Join(values, ",")
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
