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
		return out.String()
	}
	out.WriteString("functions:\n")
	for _, item := range summary.Functions {
		fmt.Fprintf(&out, "  %s: %s\n", item.Name, strings.Join(item.Permissions, ", "))
	}
	return out.String()
}

type unsafeSummary struct {
	Total             int
	Capabilities      map[string]int
	OtherCounts       map[string]int
	OtherCapabilities []string
	Functions         []unsafeFunctionSummary
}

type unsafeFunctionSummary struct {
	Name        string
	Permissions []string
}

func collectUnsafeSummary(result *semantic.Result) unsafeSummary {
	summary := unsafeSummary{
		Capabilities: map[string]int{},
		OtherCounts:  map[string]int{},
	}
	for _, capability := range unsafeCapabilityOrder {
		summary.Capabilities[capability] = 0
	}
	if result == nil || result.GlobalScope == nil {
		return summary
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
