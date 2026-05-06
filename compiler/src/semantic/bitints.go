package semantic

import (
	"fmt"
	"math"
	"strconv"
)

func ParseBitIntName(name string) (signed bool, width int, ok bool) {
	if len(name) < 2 {
		return false, 0, false
	}
	switch name[0] {
	case 'i':
		signed = true
	case 'u':
		signed = false
	default:
		return false, 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 1 || n > 64 {
		return false, 0, false
	}
	return signed, n, true
}

func BitIntName(signed bool, width int) string {
	if signed {
		return fmt.Sprintf("i%d", width)
	}
	return fmt.Sprintf("u%d", width)
}

func StandardBitIntName(signed bool, width int) bool {
	switch width {
	case 8, 16, 32, 64:
		return true
	default:
		return false
	}
}

func BitIntInfo(t Type) (signed bool, width int, ok bool) {
	switch tt := t.(type) {
	case *BitIntType:
		return tt.Signed, tt.Width, true
	case *BuiltinType:
		return ParseBitIntName(tt.Name)
	case *IDType:
		return BitIntInfo(tt.Storage)
	default:
		return false, 0, false
	}
}

func IntegerTypeFitsValue(t Type, value int64) bool {
	signed, width, ok := BitIntInfo(t)
	if !ok {
		return true
	}
	if signed {
		if width >= 64 {
			return true
		}
		min := -(int64(1) << (width - 1))
		max := (int64(1) << (width - 1)) - 1
		return value >= min && value <= max
	}
	if value < 0 {
		return false
	}
	if width >= 63 {
		return true
	}
	max := (int64(1) << width) - 1
	return value <= max
}

func InferBitIntTypeForValues(values []int64) *BitIntType {
	if len(values) == 0 {
		return &BitIntType{Signed: false, Width: 1}
	}
	minValue := values[0]
	maxValue := values[0]
	for _, value := range values[1:] {
		if value < minValue {
			minValue = value
		}
		if value > maxValue {
			maxValue = value
		}
	}
	if minValue < 0 {
		for width := 1; width <= 64; width++ {
			if IntegerTypeFitsValue(&BitIntType{Signed: true, Width: width}, minValue) &&
				IntegerTypeFitsValue(&BitIntType{Signed: true, Width: width}, maxValue) {
				return &BitIntType{Signed: true, Width: width}
			}
		}
		return &BitIntType{Signed: true, Width: 64}
	}
	for width := 1; width <= 64; width++ {
		if IntegerTypeFitsValue(&BitIntType{Signed: false, Width: width}, maxValue) {
			return &BitIntType{Signed: false, Width: width}
		}
	}
	return &BitIntType{Signed: false, Width: 64}
}

func BitGroupBackingWidth(bits int) int {
	switch {
	case bits <= 8:
		return 8
	case bits <= 16:
		return 16
	case bits <= 32:
		return 32
	default:
		return 64
	}
}

func BitWidthMaxUnsigned(width int) uint64 {
	if width >= 64 {
		return math.MaxUint64
	}
	return (uint64(1) << width) - 1
}
