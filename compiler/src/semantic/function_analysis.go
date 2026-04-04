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
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AsRefAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign-as-ref")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AugAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "aug-assign")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.ReturnStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrReturn, Note: "return"})
	case *ast.ExprStmt:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.RegionStmt:
		appendBasicFlowExprInstrs(block, n.Capacity)
	case *ast.MarkStmt:
	case *ast.RestoreStmt:
	case *ast.ResetStmt:
	case *ast.DestroyStmt:
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
	case *ast.AddrOfExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.BinaryExpr:
		appendBasicFlowExprInstrs(block, n.Left)
		appendBasicFlowExprInstrs(block, n.Right)
	case *ast.UnaryExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.CallExpr:
		appendBasicFlowExprInstrs(block, n.Func)
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.FieldExpr:
		appendBasicFlowExprInstrs(block, n.Object)
	case *ast.IndexExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Index)
	case *ast.SliceExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Start)
		appendBasicFlowExprInstrs(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
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
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	}
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
	case *ast.InStoreStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.ForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Step)
	case *ast.IterForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Source)
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
	case *ast.SliceExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.appendImplicitSinkFlowInstrsForExpr(block, arg)
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
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
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
	populateBasicFlowInstrs(cfg)
	a.addImplicitSinkFlowInstrs(cfg)
	partitions := computeGraphPartitionsFromCFG(cfg)
	cleanupPlan := SynthesizeParamCleanupPlan(fn, fnType)
	returnIsolation := summarizeReturnIsolation(fn, fnType, partitions)
	fnType.ReturnIsolation = returnIsolation
	fnType.ReturnIsolationKnown = true
	analysis := &FunctionAnalysis{
		CFG:             cfg,
		Partitions:      partitions,
		CleanupPlan:     cleanupPlan,
		SinkParams:      append([]bool(nil), fnType.SinkParams...),
		ReturnIsolation: returnIsolation,
	}
	a.functionAnalyses[fn] = analysis
}

func summarizeReturnIsolation(fn *ast.FuncDecl, fnType *FuncType, partitions *GraphPartitions) ReturnIsolationSummary {
	summary := ReturnIsolationSummary{Known: true}
	if fn == nil || fnType == nil {
		return summary
	}
	aliasLocations := map[string]bool{}
	forEachRegionParamDep(fnType.ReturnProvenance, func(index int) {
		if name := functionParamName(fn, index); name != "" {
			aliasLocations[name] = true
		}
	})
	collectBorrowedOwnerAliasLocations(fn, fnType.ReturnBorrowedOwnerRefs, aliasLocations)
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
		index := functionParamIndex(fn, root)
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

func collectBorrowedOwnerAliasLocations(fn *ast.FuncDecl, summary borrowedOwnerRefSummary, out map[string]bool) {
	if fn == nil || out == nil {
		return
	}
	if summary.HasDirect {
		if location := borrowedOwnerAliasLocation(fn, summary.Direct); location != "" {
			out[location] = true
		}
	}
	for _, child := range summary.Fields {
		collectBorrowedOwnerAliasLocations(fn, child, out)
	}
}

func borrowedOwnerAliasLocation(fn *ast.FuncDecl, target borrowedOwnerRefSummaryTarget) string {
	base := functionParamName(fn, target.ParamIndex)
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

func functionParamName(fn *ast.FuncDecl, index int) string {
	if fn == nil || index < 0 || index >= len(fn.Params) {
		return ""
	}
	return fn.Params[index].Name
}

func functionParamIndex(fn *ast.FuncDecl, name string) int {
	if fn == nil || name == "" {
		return -1
	}
	for i, param := range fn.Params {
		if param.Name == name {
			return i
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
