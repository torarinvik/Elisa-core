package ast

// SynthesizedPreReserveStmts returns compiler-created reserve preludes attached to a loop.
// They are optimization/runtime-shaping statements, not user-authored source statements.
func SynthesizedPreReserveStmts(node Node) []Stmt {
	var single Stmt
	var many []Stmt
	switch n := node.(type) {
	case *ForStmt:
		if n == nil {
			return nil
		}
		single = n.PreReserve
		many = n.PreReserves
	case *IterForStmt:
		if n == nil {
			return nil
		}
		single = n.PreReserve
		many = n.PreReserves
	default:
		return nil
	}
	if single == nil && len(many) == 0 {
		return nil
	}
	out := make([]Stmt, 0, len(many)+1)
	if single != nil {
		out = append(out, single)
	}
	out = append(out, many...)
	return out
}

// EmittedPreReserveStmts is SynthesizedPreReserveStmts minus analysis-only reserves. Codegen uses
// this; analysis and effect validation keep the full list.
func EmittedPreReserveStmts(node Node) []Stmt {
	all := SynthesizedPreReserveStmts(node)
	out := all[:0:0]
	for _, stmt := range all {
		if es, ok := stmt.(*ExprStmt); ok && es.AnalysisOnly {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

func HasSynthesizedPreReserveStmts(node Node) bool {
	switch n := node.(type) {
	case *ForStmt:
		return n != nil && (n.PreReserve != nil || len(n.PreReserves) != 0)
	case *IterForStmt:
		return n != nil && (n.PreReserve != nil || len(n.PreReserves) != 0)
	default:
		return false
	}
}
