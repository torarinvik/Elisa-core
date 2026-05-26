package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

var unsafeCapabilityOrder = []string{
	"PointerCast",
	"PointerArithmetic",
	"IndirectCall",
	"UncheckedIndex",
	"RawExtern",
	"MutableGlobal",
	"ThreadShare",
	"StaleRef",
	"Alias",
	"BufferReinterpret",
	"Assembly",
	"NonProgress",
	"BlockMain",
	"AssumeProgress",
}

func generateUnsafeReport(result *semantic.Result) string {
	summary := collectUnsafeSummary(result)
	var out bytes.Buffer
	out.WriteString("=== unsafe ===\n")
	fmt.Fprintf(&out, "total: %d\n", summary.Total)
	out.WriteString("capabilities:\n")
	for _, capability := range unsafeCapabilityOrder {
		fmt.Fprintf(&out, "  Unsafe.%s: %d\n", capability, summary.Capabilities[capability])
	}
	for _, capability := range summary.OtherCapabilities {
		fmt.Fprintf(&out, "  %s: %d\n", capability, summary.OtherCounts[capability])
	}
	if len(summary.Functions) == 0 {
		out.WriteString("functions: none\n")
	} else {
		out.WriteString("functions:\n")
		for _, item := range summary.Functions {
			fmt.Fprintf(&out, "  %s: %s\n", item.Name, strings.Join(item.Permissions, ", "))
		}
	}
	fmt.Fprintf(&out, "trusted-total: %d\n", summary.TrustedTotal)
	if len(summary.TrustedUses) == 0 {
		out.WriteString("trusted: none\n")
	} else {
		out.WriteString("trusted:\n")
		for _, capability := range unsafeCapabilityOrder {
			name := "Unsafe." + capability
			if summary.TrustedCounts[name] != 0 {
				fmt.Fprintf(&out, "  %s: %d\n", name, summary.TrustedCounts[name])
			}
		}
		for _, use := range summary.TrustedUses {
			fmt.Fprintf(&out, "  %s: %s\n", use.Function, strings.Join(use.Permissions, ", "))
		}
	}
	return out.String()
}

type unsafeSummary struct {
	Total             int
	Capabilities      map[string]int
	OtherCounts       map[string]int
	OtherCapabilities []string
	Functions         []unsafeFunctionSummary
	TrustedTotal      int
	TrustedCounts     map[string]int
	TrustedUses       []unsafeTrustedSummary
}

type unsafeFunctionSummary struct {
	Name        string
	Permissions []string
}

type unsafeTrustedSummary struct {
	Function    string
	Permissions []string
}

func collectUnsafeSummary(result *semantic.Result) unsafeSummary {
	summary := unsafeSummary{
		Capabilities:  map[string]int{},
		OtherCounts:   map[string]int{},
		TrustedCounts: map[string]int{},
	}
	for _, capability := range unsafeCapabilityOrder {
		summary.Capabilities[capability] = 0
	}
	if result == nil || result.GlobalScope == nil {
		return summary
	}
	summary.TrustedUses = collectTrustedUnsafeUses(result.ActiveFile())
	for _, use := range summary.TrustedUses {
		for _, permission := range use.Permissions {
			summary.TrustedTotal++
			summary.TrustedCounts[permission]++
		}
	}
	names := make([]string, 0, len(result.GlobalScope.Symbols))
	for name, sym := range result.GlobalScope.Symbols {
		if sym == nil || (sym.Kind != semantic.SymbolFunc && sym.Kind != semantic.SymbolExternFunc) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sym, _ := result.GlobalScope.Lookup(name)
		fnType, ok := sym.Type.(*semantic.FuncType)
		if !ok || fnType == nil {
			continue
		}
		permissions := unsafePermissionNames(fnType.PermissionRefs)
		if len(permissions) == 0 {
			continue
		}
		summary.Functions = append(summary.Functions, unsafeFunctionSummary{Name: name, Permissions: permissions})
		for _, permission := range permissions {
			summary.Total++
			if strings.HasPrefix(permission, "Unsafe.") {
				member := strings.TrimPrefix(permission, "Unsafe.")
				if _, known := summary.Capabilities[member]; known {
					summary.Capabilities[member]++
					continue
				}
			}
			summary.OtherCounts[permission]++
		}
	}
	for _, module := range result.EASMModules {
		if module == nil {
			continue
		}
		for i := range module.Functions {
			fn := &module.Functions[i]
			permissions := []string{"Unsafe.Assembly"}
			for _, req := range fn.Requires {
				req = strings.TrimSpace(req)
				if req == "" {
					continue
				}
				permissions = append(permissions, "EASM.Requires."+req)
			}
			sort.Strings(permissions)
			summary.Functions = append(summary.Functions, unsafeFunctionSummary{Name: "easm:" + fn.Name, Permissions: permissions})
			for _, permission := range permissions {
				summary.Total++
				if permission == "Unsafe.Assembly" {
					summary.Capabilities["Assembly"]++
					continue
				}
				summary.OtherCounts[permission]++
			}
		}
	}
	summary.OtherCapabilities = make([]string, 0, len(summary.OtherCounts))
	for capability := range summary.OtherCounts {
		summary.OtherCapabilities = append(summary.OtherCapabilities, capability)
	}
	sort.Strings(summary.OtherCapabilities)
	sort.Slice(summary.Functions, func(i, j int) bool {
		return summary.Functions[i].Name < summary.Functions[j].Name
	})
	return summary
}

func collectTrustedUnsafeUses(file *ast.File) []unsafeTrustedSummary {
	if file == nil {
		return nil
	}
	var out []unsafeTrustedSummary
	for _, decl := range file.Decls {
		collectTrustedUnsafeUsesFromDecl(decl, "", &out)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Function == out[j].Function {
			return strings.Join(out[i].Permissions, ",") < strings.Join(out[j].Permissions, ",")
		}
		return out[i].Function < out[j].Function
	})
	return out
}

func collectTrustedUnsafeUsesFromDecl(decl ast.Decl, namespace string, out *[]unsafeTrustedSummary) {
	switch n := decl.(type) {
	case *ast.FuncDecl:
		name := n.Name
		if namespace != "" {
			name = namespace + "." + name
		}
		collectTrustedUnsafeUsesFromStmts(n.Body, name, out)
	case *ast.NamespaceDecl:
		next := n.Name
		if namespace != "" {
			next = namespace + "." + next
		}
		for _, child := range n.Decls {
			collectTrustedUnsafeUsesFromDecl(child, next, out)
		}
	case *ast.ImplDecl:
		for _, member := range n.Members {
			if fn, ok := member.(*ast.FuncDecl); ok {
				name := fn.Name
				if namespace != "" {
					name = namespace + "." + name
				}
				collectTrustedUnsafeUsesFromStmts(fn.Body, name, out)
			}
		}
	}
}

func collectTrustedUnsafeUsesFromStmts(stmts []ast.Stmt, function string, out *[]unsafeTrustedSummary) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.CanStmt:
			if n.SuppressPermissionInference {
				permissions := unsafePermissionNames(n.Permissions)
				if len(permissions) != 0 {
					*out = append(*out, unsafeTrustedSummary{Function: function, Permissions: permissions})
				}
			}
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.IfStmt:
			collectTrustedUnsafeUsesFromStmts(n.Then, function, out)
			for _, elif := range n.Elifs {
				collectTrustedUnsafeUsesFromStmts(elif.Body, function, out)
			}
			collectTrustedUnsafeUsesFromStmts(n.Else, function, out)
		case *ast.WhileStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.ForStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.IterForStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.ParallelForStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				collectTrustedUnsafeUsesFromStmts(arm.Body, function, out)
			}
		case *ast.InStoreStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.WithStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.ArgsScopeStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.CascadeStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.ScopeStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.PoolStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.LockStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		case *ast.StaticIfStmt:
			collectTrustedUnsafeUsesFromStmts(n.Then, function, out)
			for _, elif := range n.Elifs {
				collectTrustedUnsafeUsesFromStmts(elif.Body, function, out)
			}
			collectTrustedUnsafeUsesFromStmts(n.Else, function, out)
		case *ast.StaticBlockStmt:
			collectTrustedUnsafeUsesFromStmts(n.Body, function, out)
		}
	}
}

func unsafePermissionNames(refs []ast.PermissionRef) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "Unsafe" {
			continue
		}
		name := ref.Name
		if ref.Member != "" {
			name += "." + ref.Member
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
