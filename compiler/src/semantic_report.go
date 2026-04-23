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
