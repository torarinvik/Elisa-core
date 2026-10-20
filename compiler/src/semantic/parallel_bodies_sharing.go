package semantic

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// reportCrossDeclSharing prints, when ELISA_PARALLEL_SHARING_REPORT is set, how many
// AST nodes are reachable from more than one body declaration, by node type.
func reportCrossDeclSharing(bodies []scopedDecl) {
	if os.Getenv("ELISA_PARALLEL_SHARING_REPORT") == "" {
		return
	}
	owner := map[uintptr]int{}
	shared := map[string]int{}
	sharedDecls := map[int]bool{}
	for i, scoped := range bodies {
		var stmts []ast.Stmt
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			stmts = n.Body
		case *ast.ImplDecl:
			for _, m := range n.Members {
				if fn, ok := m.(*ast.FuncDecl); ok {
					stmts = append(stmts, fn.Body...)
				}
			}
		}
		walkStoreScanNodes(stmts, func(node any) {
			p := reflect.ValueOf(node).Pointer()
			if prev, ok := owner[p]; ok && prev != i {
				shared[fmt.Sprintf("%T", node)]++
				sharedDecls[i], sharedDecls[prev] = true, true
				return
			}
			owner[p] = i
		})
	}
	type kv struct {
		k string
		v int
	}
	var rows []kv
	total := 0
	for k, v := range shared {
		rows = append(rows, kv{k, v})
		total += v
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].v > rows[j].v })
	fmt.Fprintf(os.Stderr, "[parallel-sharing] %d nodes in %d decls shared across body decls (of %d nodes)\n", total, len(sharedDecls), len(owner))
	for i, r := range rows {
		if i == 15 {
			break
		}
		fmt.Fprintf(os.Stderr, "[parallel-sharing]   %7d %s\n", r.v, r.k)
	}
}
