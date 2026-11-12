package parserdesugar

import (
	"elisacore/src/ast"
	"sort"
)

func WithBindingNameSet(decls []ast.Stmt) map[string]bool {
	set := make(map[string]bool, len(decls))
	for _, d := range decls {
		if vd, ok := d.(*ast.VarDeclStmt); ok {
			set[vd.Name] = true
		}
	}
	return set
}

// diffNameSets reports names present in want but not got (missing) and in got but not want
// (extra); ok is true when the sets are equal.
func DiffNameSets(want, got map[string]bool) (missing, extra []string, ok bool) {
	for n := range want {
		if !got[n] {
			missing = append(missing, n)
		}
	}
	for n := range got {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra, len(missing) == 0 && len(extra) == 0
}

func WithBindingParityMessage(missing, extra []string) string {
	msg := "every alternative of a `with`-arm must bind the same constants, so the shared body can read them uniformly"
	if len(missing) > 0 {
		msg += "; this alternative is missing " + quoteNameList(missing)
	}
	if len(extra) > 0 {
		msg += "; this alternative also binds " + quoteNameList(extra) + " which the first alternative does not"
	}
	return msg
}

func quoteNameList(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += "`" + n + "`"
	}
	return out
}

func MatchArmAlternativeBody(withDecls, body []ast.Stmt) []ast.Stmt {
	if len(withDecls) == 0 {
		return body
	}
	combined := make([]ast.Stmt, 0, len(withDecls)+len(body))
	combined = append(combined, withDecls...)
	combined = append(combined, body...)
	return combined
}
