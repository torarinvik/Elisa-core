package backend

import "fmt"

type OptimizationLevel int

const (
	OptimizationLevel0 OptimizationLevel = 0
	OptimizationLevel2 OptimizationLevel = 2
	OptimizationLevel3 OptimizationLevel = 3
)

func ParseOptimizationLevel(value int) (OptimizationLevel, error) {
	switch OptimizationLevel(value) {
	case OptimizationLevel0, OptimizationLevel2, OptimizationLevel3:
		return OptimizationLevel(value), nil
	default:
		return 0, fmt.Errorf("unsupported optimization level O%d (expected O0, O2, or O3)", value)
	}
}
