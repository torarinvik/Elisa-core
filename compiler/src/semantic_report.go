package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/semantic"
	"llcontext/src/unparse"
)

func generateSemanticReport(result *semantic.Result) string {
	if result == nil {
		return ""
	}
	activeFile := result.ActiveFile()
	var out bytes.Buffer
	if activeFile != nil {
		out.WriteString("=== lowered ===\n")
		out.WriteString(unparse.FormatFile(activeFile))
		if out.Len() == 0 || !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
	}
	out.WriteString("=== semantic ===\n")
	if result.GlobalScope == nil || len(result.GlobalScope.Symbols) == 0 {
		out.WriteString("<no global symbols>\n")
		return out.String()
	}

	funcNames := make([]string, 0)
	for name, sym := range result.GlobalScope.Symbols {
		if sym == nil {
			continue
		}
		if sym.Kind != semantic.SymbolFunc && sym.Kind != semantic.SymbolExternFunc {
			continue
		}
		funcNames = append(funcNames, name)
	}
	sort.Strings(funcNames)
	for _, name := range funcNames {
		sym := result.GlobalScope.Symbols[name]
		fnType, ok := sym.Type.(*semantic.FuncType)
		if !ok || fnType == nil {
			fmt.Fprintf(&out, "func %s\n", name)
			fmt.Fprintf(&out, "  signature: <invalid>\n")
			continue
		}
		fmt.Fprintf(&out, "func %s\n", name)
		fmt.Fprintf(&out, "  signature: %s\n", fnType.String())
		if annotations := symbolAnnotations(sym.Node); len(annotations) != 0 {
			fmt.Fprintf(&out, "  annotations: %s\n", strings.Join(annotations, ", "))
		}
		if analysis, ok := result.FunctionAnalysisByName(name); ok && analysis != nil {
			fmt.Fprintf(&out, "  sink_params: %s\n", summarizeSinkParams(analysis.SinkParams))
			fmt.Fprintf(&out, "  return_isolation: %s\n", summarizeReturnIsolation(analysis.ReturnIsolation))
			if snapshot := semantic.FormatFactSnapshot(analysis.FactSnapshot); snapshot != "" {
				fmt.Fprintf(&out, "  fact_snapshot: %s\n", snapshot)
			}
			if exits := semantic.FormatFactExitSummary(analysis.FactExitSummary); exits != "" {
				fmt.Fprintf(&out, "  fact_exits: %s\n", exits)
			}
			if aliases := semantic.FormatFactAliasSets(analysis.AliasSets); aliases != "" {
				fmt.Fprintf(&out, "  fact_aliases:\n%s", indentReportBlock(aliases, "    "))
			}
			if summary := semantic.FormatFactTransforms(analysis.FactTransforms); summary != "" {
				fmt.Fprintf(&out, "  fact_transforms: %s\n", summary)
			}
			if groups := semantic.FormatFactTransformGroups(analysis.FactTransforms); groups != "" {
				fmt.Fprintf(&out, "  fact_groups:\n%s", indentReportBlock(groups, "    "))
			}
			if explanations := semantic.FormatFactExplanations(analysis.FactTransforms); explanations != "" {
				fmt.Fprintf(&out, "  fact_explanations:\n%s", indentReportBlock(explanations, "    "))
			}
			if blockSummary := summarizeBlockFactTransforms(analysis.BlockFactTransforms); blockSummary != "" {
				fmt.Fprintf(&out, "  fact_blocks:\n%s", indentReportBlock(blockSummary, "    "))
			}
		}
	}
	if len(result.ExportedFuncs) != 0 || len(result.ExportedGlobals) != 0 || len(result.ExportedTypes) != 0 {
		out.WriteString("exports\n")
		for _, exported := range result.ExportedTypes {
			if exported == nil || exported.Type == nil {
				continue
			}
			fmt.Fprintf(&out, "  type %s = %s\n", exported.PublicName, exported.Type.String())
		}
		for _, exported := range result.ExportedFuncs {
			if exported == nil || exported.Signature == nil {
				continue
			}
			fmt.Fprintf(&out, "  func %s: %s\n", exported.PublicName, exported.Signature.String())
		}
		for _, exported := range result.ExportedGlobals {
			if exported == nil || exported.Type == nil {
				continue
			}
			fmt.Fprintf(&out, "  global %s: %s\n", exported.PublicName, exported.Type.String())
		}
	}
	return out.String()
}

func generateFactTraceReport(result *semantic.Result, filter string) string {
	if result == nil || result.GlobalScope == nil {
		return ""
	}
	var out bytes.Buffer
	out.WriteString("=== facts ===\n")
	funcNames := make([]string, 0)
	for name, sym := range result.GlobalScope.Symbols {
		if sym == nil || (sym.Kind != semantic.SymbolFunc && sym.Kind != semantic.SymbolExternFunc) {
			continue
		}
		if filter != "" && !strings.Contains(name, filter) {
			continue
		}
		funcNames = append(funcNames, name)
	}
	sort.Strings(funcNames)
	for _, name := range funcNames {
		analysis, ok := result.FunctionAnalysisByName(name)
		if !ok || analysis == nil {
			continue
		}
		fmt.Fprintf(&out, "func %s\n", name)
		if snapshot := semantic.FormatFactSnapshot(analysis.FactSnapshot); snapshot != "" {
			fmt.Fprintf(&out, "  snapshot: %s\n", snapshot)
		}
		if exits := semantic.FormatFactExitSummary(analysis.FactExitSummary); exits != "" {
			fmt.Fprintf(&out, "  exits: %s\n", exits)
		}
		if aliases := semantic.FormatFactAliasSets(analysis.AliasSets); aliases != "" {
			fmt.Fprintf(&out, "  aliases:\n%s", indentReportBlock(aliases, "    "))
		}
		if groups := semantic.FormatFactTransformGroups(analysis.FactTransforms); groups != "" {
			fmt.Fprintf(&out, "  groups:\n%s", indentReportBlock(groups, "    "))
		}
		if explanations := semantic.FormatFactExplanations(analysis.FactTransforms); explanations != "" {
			fmt.Fprintf(&out, "  explanations:\n%s", indentReportBlock(explanations, "    "))
		}
	}
	return out.String()
}

func summarizeBlockFactTransforms(blocks []semantic.FactBlockTransforms) string {
	if len(blocks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if len(block.Transforms) == 0 {
			continue
		}
		if summary := semantic.FormatFactTransforms(block.Transforms); summary != "" {
			lines = append(lines, fmt.Sprintf("block %d: %s", block.BlockID, summary))
		}
	}
	return strings.Join(lines, "\n")
}

func indentReportBlock(text string, prefix string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func summarizeSinkParams(sinkParams []bool) string {
	if len(sinkParams) == 0 {
		return "[]"
	}
	indices := make([]string, 0)
	for i, sink := range sinkParams {
		if sink {
			indices = append(indices, fmt.Sprintf("%d", i))
		}
	}
	if len(indices) == 0 {
		return "[]"
	}
	return "[" + strings.Join(indices, ", ") + "]"
}

func summarizeReturnIsolation(summary semantic.ReturnIsolationSummary) string {
	if !summary.Known {
		return "unknown"
	}
	if summary.Isolated && len(summary.AliasParamIndices) == 0 && len(summary.AliasLocations) == 0 {
		return "isolated"
	}
	parts := make([]string, 0, 3)
	if len(summary.AliasParamIndices) != 0 {
		parts = append(parts, "alias_params="+formatIntSlice(summary.AliasParamIndices))
	}
	if len(summary.AliasMutableParamIndices) != 0 {
		parts = append(parts, "alias_mutable_params="+formatIntSlice(summary.AliasMutableParamIndices))
	}
	if len(summary.AliasLocations) != 0 {
		parts = append(parts, "alias_locations=["+strings.Join(summary.AliasLocations, ", ")+"]")
	}
	if len(parts) == 0 {
		return "non-isolated"
	}
	return strings.Join(parts, " ")
}

func formatIntSlice(values []int) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func symbolAnnotations(node ast.Node) []string {
	if node == nil {
		return nil
	}
	var annotations []ast.Annotation
	switch n := node.(type) {
	case *ast.FuncDecl:
		annotations = n.Annotations
	case *ast.ExternFuncDecl:
		annotations = n.Annotations
	default:
		return nil
	}
	out := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		out = append(out, formatAnnotation(annotation))
	}
	return out
}
