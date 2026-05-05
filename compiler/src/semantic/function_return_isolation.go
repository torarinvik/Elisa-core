package semantic

import (
	"elisacore/src/ast"
	"sort"
	"strings"
)

func summarizeReturnIsolation(fn *ast.FuncDecl, fnType *FuncType, partitions *GraphPartitions) ReturnIsolationSummary {
	summary := ReturnIsolationSummary{Known: true}
	if fn == nil || fnType == nil {
		return summary
	}
	aliasLocations := map[string]bool{}
	forEachRegionParamDep(fnType.ReturnProvenance, func(index int) {
		if name := functionParamName(fnType, index); name != "" {
			aliasLocations[name] = true
		}
	})
	collectBorrowedOwnerAliasLocations(fnType, fnType.ReturnBorrowedOwnerRefs, aliasLocations)
	if len(aliasLocations) == 0 {
		summary.Isolated = true
		return summary
	}
	summary.Isolated = false
	paramAliases := map[int]bool{}
	mutableAliases := map[int]bool{}
	for location := range aliasLocations {
		summary.AliasLocations = append(summary.AliasLocations, location)
		root := flowLocationRoot(location)
		index := functionParamIndex(fnType, root)
		if index >= 0 {
			paramAliases[index] = true
			if partitions != nil && partitions.ClassMutated(location) {
				mutableAliases[index] = true
			}
		}
	}
	sort.Strings(summary.AliasLocations)
	for index := range paramAliases {
		summary.AliasParamIndices = append(summary.AliasParamIndices, index)
	}
	for index := range mutableAliases {
		summary.AliasMutableParamIndices = append(summary.AliasMutableParamIndices, index)
	}
	sort.Ints(summary.AliasParamIndices)
	sort.Ints(summary.AliasMutableParamIndices)
	return summary
}

func collectBorrowedOwnerAliasLocations(fnType *FuncType, summary borrowedOwnerRefSummary, out map[string]bool) {
	if fnType == nil || out == nil {
		return
	}
	if summary.HasDirect {
		if location := borrowedOwnerAliasLocation(fnType, summary.Direct); location != "" {
			out[location] = true
		}
	}
	for _, child := range summary.Fields {
		collectBorrowedOwnerAliasLocations(fnType, child, out)
	}
}

func borrowedOwnerAliasLocation(fnType *FuncType, target borrowedOwnerRefSummaryTarget) string {
	base := functionParamName(fnType, target.ParamIndex)
	if base == "" {
		return ""
	}
	return base + borrowAnnotationPathSuffix(target.Path)
}

func borrowAnnotationPathSuffix(path []borrowReturnAnnotationStep) string {
	if len(path) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, step := range path {
		switch {
		case step.Field != "":
			builder.WriteByte('.')
			builder.WriteString(step.Field)
		case step.Wildcard:
			builder.WriteString("[*]")
		case step.Index != nil:
			builder.WriteString("[")
			builder.WriteString(strconvFormatInt(*step.Index))
			builder.WriteString("]")
		}
	}
	return builder.String()
}

func functionParamName(fnType *FuncType, index int) string {
	if fnType == nil || index < 0 || index >= len(fnType.Params) {
		return ""
	}
	if index < len(fnType.ExplicitParamNames) {
		return fnType.ExplicitParamNames[index]
	}
	implicitIndex := index - len(fnType.ExplicitParamNames)
	if implicitIndex >= 0 && implicitIndex < len(fnType.ImplicitParamNames) {
		return fnType.ImplicitParamNames[implicitIndex]
	}
	return ""
}

func functionParamIndex(fnType *FuncType, name string) int {
	if fnType == nil || name == "" {
		return -1
	}
	for i, paramName := range fnType.ExplicitParamNames {
		if paramName == name {
			return i
		}
	}
	for i, paramName := range fnType.ImplicitParamNames {
		if paramName == name {
			return len(fnType.ExplicitParamNames) + i
		}
	}
	return -1
}

func dedupeStringSlice(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func returnIsolationSummariesEqual(left ReturnIsolationSummary, right ReturnIsolationSummary) bool {
	if left.Known != right.Known || left.Isolated != right.Isolated {
		return false
	}
	if len(left.AliasLocations) != len(right.AliasLocations) || len(left.AliasParamIndices) != len(right.AliasParamIndices) || len(left.AliasMutableParamIndices) != len(right.AliasMutableParamIndices) {
		return false
	}
	for i := range left.AliasLocations {
		if left.AliasLocations[i] != right.AliasLocations[i] {
			return false
		}
	}
	for i := range left.AliasParamIndices {
		if left.AliasParamIndices[i] != right.AliasParamIndices[i] {
			return false
		}
	}
	for i := range left.AliasMutableParamIndices {
		if left.AliasMutableParamIndices[i] != right.AliasMutableParamIndices[i] {
			return false
		}
	}
	return true
}

func strconvFormatInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + (value % 10))
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
