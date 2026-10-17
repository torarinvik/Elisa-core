package unparse

import (
	"elisacore/src/ast"
	"strings"
)

func (f *formatter) writeContractConditions(level int, keyword string, conditions []ast.Expr, proofs []*ast.ProofBlockStmt) {
	for i, condition := range conditions {
		line := keyword + " " + formatExpr(condition)
		var proof *ast.ProofBlockStmt
		if i < len(proofs) {
			proof = proofs[i]
		}
		if proof != nil {
			line += " by scoped:"
		}
		f.writeLine(level, line)
		if proof != nil {
			for _, stmt := range proof.Proof {
				f.writeStmt(level+1, stmt)
			}
		}
	}
}

func (f *formatter) writeNamedContract(level int, n *ast.FuncDecl) {
	f.writeAnnotations(level, n.Annotations)
	header := "contract " + n.Name + formatGenericParams(n.GenericParams, n.TypeParams, n.RegionParams, n.PermissionParams)
	f.writeLine(level, header+"("+formatExplicitParamList(n.Params, false)+"):")
	f.writeContractConditions(level+1, "requires", n.Requires, n.RequiresProofs)
	f.writeContractConditions(level+1, "ensure", n.EnsureValues, n.EnsureProofs)
	f.writeFrameContractLines(level+1, n.Changes, n.Preserves)
	for _, include := range n.ContractIncludes {
		args := make([]string, len(include.Args))
		for i, arg := range include.Args {
			args[i] = formatExpr(arg)
		}
		f.writeLine(level+1, "includes "+include.Law+"("+strings.Join(args, ", ")+")")
	}
}

func (f *formatter) writeFrameContractLines(level int, changes, preserves []ast.EnsuresPath) {
	for _, clause := range []struct {
		keyword string
		paths   []ast.EnsuresPath
	}{{"changes", changes}, {"preserves", preserves}} {
		if len(clause.paths) == 0 {
			continue
		}
		paths := make([]string, len(clause.paths))
		for i, path := range clause.paths {
			paths[i] = formatEnsuresPath(path)
		}
		f.writeLine(level, clause.keyword+" "+strings.Join(paths, ", "))
	}
}

func (f *formatter) writeLaw(level int, n *ast.FuncDecl) {
	f.writeAnnotations(level, n.Annotations)
	header := "law " + n.Name + formatGenericParams(n.GenericParams, n.TypeParams, n.RegionParams, n.PermissionParams)
	if len(n.Params) > 0 {
		header += "(" + formatExplicitParamList(n.Params, false) + ")"
	}
	switch {
	case len(n.Forbids) > 0:
		parts := make([]string, len(n.Forbids))
		for i, ref := range n.Forbids {
			parts[i] = formatPermissionRef(ref)
		}
		f.writeLine(level, header+" forbids "+strings.Join(parts, ", "))
	case len(n.Includes) > 0:
		f.writeLine(level, header+" includes "+strings.Join(n.Includes, ", "))
	case len(n.Changes) > 0 || len(n.Preserves) > 0:
		f.writeLine(level, header+formatBoundaryFrames(n.Changes, n.Preserves))
	default:
		// Parsed value/block laws have a predicate in a return statement. Keeping
		// the block form also accommodates semantic rewriting of that expression.
		f.writeLine(level, header+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	}
}
