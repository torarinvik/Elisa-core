package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/taskgroupsyntax"
	"reflect"
)

type taskGroupNodeFacts struct {
	groupRef        bool
	groupAPI        bool
	untrustedCall   bool
	safeSubmitCount int
	earlyExit       bool
	groupShadow     bool
	apiShadow       bool
}

type taskGroupSubmitCandidate struct {
	call         *ast.CallExpr
	alreadyAdded bool
}

func taskGroupFacts(node any, groupName string) taskGroupNodeFacts {
	facts := taskgroupsyntax.CollectFacts(node, groupName, func(value any, visit func(any)) { fillMayAdoptWalk(reflect.ValueOf(value), visit) })
	return taskGroupNodeFacts{groupRef: facts.GroupRef, groupAPI: facts.GroupAPI, untrustedCall: facts.UntrustedCall, safeSubmitCount: facts.SafeSubmitCount, earlyExit: facts.EarlyExit, groupShadow: facts.GroupShadow, apiShadow: facts.ApiShadow}
}

func taskGroupWindowStmtSafe(stmt ast.Stmt, groupName string, submits *[]taskGroupSubmitCandidate) bool {
	return taskgroupsyntax.WindowStmtSafe(stmt, groupName, func(value any, visit func(any)) { fillMayAdoptWalk(reflect.ValueOf(value), visit) }, func(call *ast.CallExpr) {
		*submits = append(*submits, taskGroupSubmitCandidate{call: call, alreadyAdded: true})
	})
}

func taskGroupRangeMentionsName(stmts []ast.Stmt, name string) bool {
	for _, stmt := range stmts {
		facts := taskGroupFacts(stmt, name)
		if facts.groupRef || facts.groupShadow {
			return true
		}
	}
	return false
}

func taskGroupRangeShadowsAPI(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		facts := taskGroupFacts(stmt, "")
		if facts.apiShadow {
			return true
		}
	}
	return false
}

func taskGroupMovePatternShadowsAPI(pattern ast.MoveBindPattern) bool {
	return taskgroupsyntax.MovePatternShadowsAPI(pattern)
}
func taskGroupMatchPatternShadowsAPI(pattern ast.MatchPattern) bool {
	return taskgroupsyntax.MatchPatternShadowsAPI(pattern)
}
func walkTaskGroupStmtLists(body []ast.Stmt, visit func([]ast.Stmt)) {
	taskgroupsyntax.WalkStmtLists(body, visit)
}
