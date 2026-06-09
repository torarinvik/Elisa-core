package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
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
	"ExecutableCodePublish",
	"GuestHostPointerCast",
	"PoisonPointerSentinel",
	"TinyPointerSentinel",
	"SegmentMutation",
	"GuestSegmentInstall",
	"CrossThreadSignalJump",
	"MachineCodeBuilder",
	"NonProgress",
	"BlockMain",
	"AssumeProgress",
}

var boundaryInvariantRegistry = []boundaryInvariant{
	{
		Name:        "GuestHostPointer",
		Triggers:    []string{"Unsafe.PointerCast", "Unsafe.PointerArithmetic", "Unsafe.GuestHostPointerCast"},
		StaticRule:  "guest addresses must resolve to a proven host/native-mapped pointer before dereference",
		TraceRule:   "trace every guest-to-host address resolve with address, length, mapping, and result",
		RuntimeRule: "debug/referee mode traps unresolved, unmapped, poison, or near-null address use",
	},
	{
		Name:        "ExecutableCodePublish",
		Triggers:    []string{"Unsafe.ExecutableCodePublish", "Unsafe.MachineCodeBuilder", "Unsafe.Assembly", "EASM.Requires.control.indirect"},
		StaticRule:  "runtime executable code must flow through a named publish primitive before call/jump",
		TraceRule:   "trace publish address, size, protection, cache/publish result, and first execution",
		RuntimeRule: "debug/referee mode halts execution of unpublished generated code",
	},
	{
		Name:        "MachineSegmentState",
		Triggers:    []string{"Unsafe.SegmentMutation", "Unsafe.GuestSegmentInstall", "EASM.Requires.x86_64.segment.fs", "EASM.Requires.x86_64.segment.gs", "EASM.Requires.x86_64.segment.restore", "EASM.Requires.x86_64.segment.persistent"},
		StaticRule:  "segment register writes require an explicit restore/persistent contract",
		TraceRule:   "trace every host/guest segment switch with thread id and selector",
		RuntimeRule: "debug/referee mode asserts expected segment state at guest/host boundaries",
	},
	{
		Name:        "TinyCallable",
		Triggers:    []string{"Unsafe.IndirectCall", "Unsafe.PoisonPointerSentinel", "Unsafe.TinyPointerSentinel", "EASM.Requires.control.indirect"},
		StaticRule:  "callable targets must not be poison or near-null unless explicitly marked sentinel",
		TraceRule:   "trace every dynamic callable materialization with source slot/symbol provenance",
		RuntimeRule: "debug/referee mode halts poison, non-canonical, or near-null call/jump targets",
	},
	{
		Name:        "ThreadAffineSignalJump",
		Triggers:    []string{"Unsafe.CrossThreadSignalJump", "Unsafe.ThreadShare"},
		StaticRule:  "signal-handler longjmp requires a thread-affine buffer or explicit cross-thread trust",
		TraceRule:   "trace signal number, faulting thread, guard thread, and jump decision",
		RuntimeRule: "referee mode refuses cross-thread longjmp and records a labeled boundary fault",
	},
}

func generateUnsafeReport(result *semantic.Result) string {
	summary := collectUnsafeSummary(result)
	return generateUnsafeReportFromSummary(summary)
}

func generateUnsafeReportFromSummary(summary unsafeSummary) string {
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
	writeBoundaryInvariantReport(&out, summary)
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

func checkUnsafeBudget(summary unsafeSummary, spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	for _, rawItem := range strings.Split(spec, ",") {
		item := strings.TrimSpace(rawItem)
		if item == "" {
			continue
		}
		key, limitText, ok := strings.Cut(item, "<=")
		if !ok {
			key, limitText, ok = strings.Cut(item, "=")
		}
		if !ok {
			return fmt.Errorf("invalid unsafe budget item %q (expected name=N or name<=N)", item)
		}
		key = strings.TrimSpace(key)
		limit, err := strconv.Atoi(strings.TrimSpace(limitText))
		if err != nil || limit < 0 {
			return fmt.Errorf("invalid unsafe budget limit in %q", item)
		}
		actual, ok := unsafeBudgetCount(summary, key)
		if !ok {
			return fmt.Errorf("unknown unsafe budget key %q", key)
		}
		if actual > limit {
			return fmt.Errorf("unsafe budget exceeded for %s: %d > %d", key, actual, limit)
		}
	}
	return nil
}

func unsafeBudgetCount(summary unsafeSummary, key string) (int, bool) {
	switch key {
	case "total":
		return summary.Total, true
	case "trusted-total":
		return summary.TrustedTotal, true
	}
	if strings.HasPrefix(key, "Unsafe.") {
		member := strings.TrimPrefix(key, "Unsafe.")
		if count, ok := summary.Capabilities[member]; ok {
			return count, true
		}
		if count, ok := summary.TrustedCounts[key]; ok {
			return count, true
		}
		return 0, true
	}
	if count, ok := summary.Capabilities[key]; ok {
		return count, true
	}
	if count, ok := summary.OtherCounts[key]; ok {
		return count, true
	}
	return 0, false
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

type boundaryInvariant struct {
	Name        string
	Triggers    []string
	StaticRule  string
	TraceRule   string
	RuntimeRule string
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
				if capability := easmRequireUnsafeCapability(req); capability != "" {
					permissions = append(permissions, capability)
				}
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

func easmRequireUnsafeCapability(req string) string {
	switch strings.TrimSpace(req) {
	case "control.poison_target.unchecked":
		return "Unsafe.PoisonPointerSentinel"
	case "control.tiny_target.unchecked":
		return "Unsafe.TinyPointerSentinel"
	default:
		return ""
	}
}

func writeBoundaryInvariantReport(out *bytes.Buffer, summary unsafeSummary) {
	active := activeBoundaryInvariants(summary)
	if len(active) == 0 {
		out.WriteString("boundary-invariants: none\n")
		return
	}
	out.WriteString("boundary-invariants:\n")
	for _, invariant := range active {
		fmt.Fprintf(out, "  %s:\n", invariant.Name)
		fmt.Fprintf(out, "    static: %s\n", invariant.StaticRule)
		fmt.Fprintf(out, "    trace: %s\n", invariant.TraceRule)
		fmt.Fprintf(out, "    runtime: %s\n", invariant.RuntimeRule)
	}
}

func activeBoundaryInvariants(summary unsafeSummary) []boundaryInvariant {
	seen := map[string]bool{}
	for capability, count := range summary.Capabilities {
		if count != 0 {
			seen["Unsafe."+capability] = true
		}
	}
	for capability, count := range summary.OtherCounts {
		if count != 0 {
			seen[capability] = true
		}
	}
	for capability, count := range summary.TrustedCounts {
		if count != 0 {
			seen[capability] = true
		}
	}
	active := make([]boundaryInvariant, 0, len(boundaryInvariantRegistry))
	for _, invariant := range boundaryInvariantRegistry {
		for _, trigger := range invariant.Triggers {
			if seen[trigger] {
				active = append(active, invariant)
				break
			}
		}
	}
	return active
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
