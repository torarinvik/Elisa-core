package semantic

import "fmt"

func optimizationChunkExtentOffsetExpr(chunkSize string, chunkIndex int64) string {
	if chunkIndex <= 0 {
		return "0"
	}
	if chunkIndex == 1 {
		return chunkSize
	}
	return fmt.Sprintf("(%d * %s)", chunkIndex, chunkSize)
}
func optimizationAddOffsetExpr(base string, offset string) string {
	if base == "" || base == "0" {
		return offset
	}
	if offset == "" || offset == "0" {
		return base
	}
	if baseValue, baseOK := optimizationConstInt(base); baseOK {
		if offsetValue, offsetOK := optimizationConstInt(offset); offsetOK {
			return fmt.Sprintf("%d", baseValue+offsetValue)
		}
	}
	return fmt.Sprintf("(%s + %s)", base, offset)
}
func composeOptimizationExtentWithExactBase(base *OptimizationExtent, begin string, end string) *OptimizationExtent {
	if begin == "" || end == "" {
		return nil
	}
	if base == nil || base.Kind != OptimizationExtentViewBounds {
		return &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: end}
	}
	baseBegin := base.Begin
	if baseBegin == "" {
		baseBegin = "0"
	}
	return &OptimizationExtent{
		Kind:  OptimizationExtentViewBounds,
		Begin: optimizationAddOffsetExpr(baseBegin, begin),
		End:   optimizationAddOffsetExpr(baseBegin, end),
	}
}
func optimizationConstInt(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	trimmed := value
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	if last := trimmed[len(trimmed)-1]; last == 'u' || last == 'i' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	value64, err := parseOptimizationInt(trimmed)
	if err != nil {
		return 0, false
	}
	return value64, true
}
func parseOptimizationInt(value string) (int64, error) {
	negative := false
	if value != "" && value[0] == '-' {
		negative = true
		value = value[1:]
	}
	if value == "" {
		return 0, fmt.Errorf("empty int")
	}
	var out int64
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-decimal int %q", value)
		}
		out = out*10 + int64(ch-'0')
	}
	if negative {
		out = -out
	}
	return out, nil
}
