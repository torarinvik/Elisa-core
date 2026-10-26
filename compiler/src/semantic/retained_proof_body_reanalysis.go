package semantic

import "elisacore/src/ast"

// Proof journals can conservatively depend on foreign bodies even when a
// different analysis schedule answered that consultation from a summary. Reuse
// never drops those origins. Instead, refresh the exact body-owner dependency
// closure through the ordinary independently prepared body-splice transaction.
// Declaration/unknown/instantiated contributions retain the old refusal.
func (r *RetainedAnalysis) proofReportBodyReanalysisClosure(edited, removed []*ast.FuncDecl) (map[*ast.FuncDecl]bool, string) {
	sourceChanged := map[ast.Node]bool{}
	for _, fn := range edited {
		sourceChanged[fn] = true
	}
	for _, fn := range removed {
		sourceChanged[fn] = true
	}
	changed := map[ast.Node]bool{}
	for node := range sourceChanged {
		changed[node] = true
	}
	forced := map[*ast.FuncDecl]bool{}
	for {
		grew := false
		for key, row := range r.a.proofLifecycle.Segments {
			if len(row.Facts) == 0 || key.Phase == proofPhasePostDefault || sourceChanged[key.Owner] {
				continue
			}
			affected := false
			for origin := range row.Origins {
				// Every row has its source owner installed as an origin. Refreshing only
				// that owner's body does not modify its own declaration/header source.
				if origin == key.Owner {
					continue
				}
				if changed[origin] {
					affected = true
					break
				}
			}
			if !affected {
				continue
			}
			owner, ok := key.Owner.(*ast.FuncDecl)
			if !ok || owner == nil || key.Phase != proofPhaseBody || key.Site != nil || key.Instance != "" {
				return nil, "proof report dependent contribution requires reanalysis outside an ordinary body"
			}
			if forced[owner] {
				continue
			}
			// A body splice retires its prior body contributions. Do not pretend a
			// template-only pass reproduces already-published concrete instantiations.
			for other, contribution := range r.a.proofLifecycle.Segments {
				if other.Owner == owner && other.Phase == proofPhaseBody && len(contribution.Facts) != 0 && (other.Site != nil || other.Instance != "") {
					return nil, "proof report instantiated dependent body requires complete instance replay"
				}
			}
			forced[owner], changed[owner], grew = true, true, true
		}
		if !grew {
			return forced, ""
		}
	}
}
