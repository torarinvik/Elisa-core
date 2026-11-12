package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/literals"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// intLitRequiresSuffix reports whether an integer literal's value exceeds the int64 range
// (but still fits uint64), in which case an unsuffixed literal would fail default `int`
// inference and the explicit unsigned suffix is necessary -- so it should not be flagged as
// discouraged. Values that fit int64 (the vast majority) return false and stay lint-eligible.
func intLitRequiresSuffix(n *ast.IntLit) bool {
	if n == nil {
		return false
	}
	text := strings.ReplaceAll(n.Value, "_", "")
	base := 10
	if n.IsHex {
		base = 16
		text = strings.TrimPrefix(text, "0x")
		text = strings.TrimPrefix(text, "0X")
	}
	if _, err := strconv.ParseInt(text, base, 64); err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			if _, uerr := strconv.ParseUint(text, base, 64); uerr == nil {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) warnNumericLiteralSuffix(expr ast.Expr, suffix string) {
	if suffix == "" || expr == nil {
		return
	}
	if a.numericLiteralSuffixWarnings == nil {
		a.numericLiteralSuffixWarnings = map[ast.Expr]bool{}
	}
	if a.numericLiteralSuffixWarnings[expr] {
		return
	}
	a.numericLiteralSuffixWarnings[expr] = true
	a.warnf(expr.Pos(), "numeric literal suffix %q is discouraged; let the literal type be inferred from context or use an explicit cast", suffix)
}

func (a *Analyzer) deprecatedf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	message := fmt.Sprintf(format, formatDiagnosticArgs(args)...)
	key := fmt.Sprintf("%s:%d:%d:%s", pos.File, pos.Line, pos.Col, message)
	if a.deprecatedSeen == nil {
		a.deprecatedSeen = make(map[string]bool)
	}
	if a.deprecatedSeen[key] {
		return
	}
	a.deprecatedSeen[key] = true
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityDeprecated, Message: message})
}

func isNullableRef(t Type) bool {
	r, ok := t.(*RefType)
	return ok && r.State == RefStateNullable
}

func isRefLike(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

func ParseIntLiteral(expr *ast.IntLit) (int64, bool) {
	base := 10
	text := expr.Value
	if expr.IsHex {
		base = 0
	}
	// Unsigned-typed literals (u8/u16/u32/u64/usize) may have their top bit
	// set, which overflows a signed parse. Parse them as unsigned and store
	// the bit pattern in the int64 const value (consteval stores ints as
	// int64, matching the representation produced by e.g. ~0u64).
	if strings.HasPrefix(expr.Suffix, "u") {
		if u, err := strconv.ParseUint(text, base, 64); err == nil {
			return int64(u), true
		}
		return 0, false
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isUnsigned64CapableType reports whether a type is one of the 64-bit-wide unsigned builtins
// (u64/usize/uintptr on a 64-bit target), the only integer types that can hold a literal value
// exceeding i64 max. Peels a non-null reference wrapper.
func isUnsigned64CapableType(t Type) bool {
	b, ok := stripRefForBounds(t).(*BuiltinType)
	if !ok || b == nil {
		return false
	}
	switch b.Name {
	case "u64", "usize", "uintptr":
		return true
	}
	return false
}

// ParseIntLiteralForType parses an integer literal in the context of an expected type. It behaves
// exactly like ParseIntLiteral, EXCEPT that a suffixless literal whose value overflows signed i64 but
// fits u64 is accepted when the target is a 64-bit unsigned type — so a declared `u64`/`usize`/
// `uintptr` stands in for the explicit `u64` suffix (e.g. `global X: u64 = 0xcbf29ce484222325`). The
// bit pattern is stored in the returned int64 (matching how `~0u64` / suffixed literals are stored).
// Signed-typed contexts are unaffected: a too-large literal there still fails, so overflow stays an
// error. A nil/unknown target reduces to ParseIntLiteral.
func ParseIntLiteralForType(expr *ast.IntLit, target Type) (int64, bool) {
	if v, ok := ParseIntLiteral(expr); ok {
		return v, true
	}
	if expr == nil || strings.HasPrefix(expr.Suffix, "u") {
		// A `u`-suffixed literal already attempted ParseUint inside ParseIntLiteral; nothing more to try.
		return 0, false
	}
	if !isUnsigned64CapableType(target) {
		return 0, false
	}
	base := 10
	if expr.IsHex {
		base = 0
	}
	if u, err := strconv.ParseUint(expr.Value, base, 64); err == nil {
		return int64(u), true
	}
	return 0, false
}

func ParseFloatLiteral(expr *ast.FloatLit) (float64, bool) {
	v, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func ParseCharLiteral(expr *ast.CharLit) (int64, bool) {
	return literals.ParseCharLiteral(expr)
}

func CastConstValue(value ConstValue, dst Type) (ConstValue, bool) {
	if storage, ok := ConstEnumStorageType(dst); ok {
		dst = storage
	}
	if idType, ok := dst.(*IDType); ok {
		dst = idType.Storage
	}
	if !IsNumericType(dst) || !isConstNumeric(value) {
		return ConstValue{}, false
	}
	if IsFloatType(dst) {
		floatValue := constNumericAsFloat64(value)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return ConstValue{}, false
		}
		if builtin, ok := dst.(*BuiltinType); ok && builtin.Name == "f32" {
			return ConstValue{Kind: ConstFloat, Float: float64(float32(floatValue))}, true
		}
		return ConstValue{Kind: ConstFloat, Float: floatValue}, true
	}
	intValue, ok := castConstNumericToInt64(value, dst)
	if !ok {
		return ConstValue{}, false
	}
	return ConstValue{Kind: ConstInt, Int: intValue}, true
}

func castConstNumericToInt64(value ConstValue, dst Type) (int64, bool) {
	floatValue := constNumericAsFloat64(value)
	if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
		return 0, false
	}
	truncated := math.Trunc(floatValue)
	name, ok := builtinNumericTypeName(dst)
	if !ok {
		return 0, false
	}
	if isSignedConstCastBuiltin(name) {
		if usesInt64ConstRange(name) {
			// float64(math.MaxInt64) rounds up to 2^63, so compare against the exact
			// exclusive upper bound instead of float64(maxValue) to avoid accepting
			// out-of-range values like 9223372036854775808.0.
			if truncated < float64(math.MinInt64) || truncated >= math.Exp2(63) {
				return 0, false
			}
			return int64(truncated), true
		}
		minValue, maxValue, ok := signedConstCastRange(name)
		if !ok || truncated < float64(minValue) || truncated > float64(maxValue) {
			return 0, false
		}
		return narrowSignedConstCast(int64(truncated), name), true
	}
	if truncated < 0 {
		return 0, false
	}
	if usesInt64BackedUnsignedConstRange(name) {
		// The current ConstValue representation stores integers as int64, so
		// compile-time unsigned constants must stay below 2^63 to remain
		// representable without wrapping.
		if truncated >= math.Exp2(63) {
			return 0, false
		}
		return int64(truncated), true
	}
	maxValue, ok := unsignedConstCastMax(name)
	if !ok || truncated > float64(maxValue) {
		return 0, false
	}
	return int64(narrowUnsignedConstCast(uint64(truncated), name)), true
}

func builtinNumericTypeName(t Type) (string, bool) {
	if bit, ok := t.(*BitIntType); ok {
		return BitIntName(bit.Signed, bit.Width), true
	}
	if builtin, ok := t.(*BuiltinType); ok {
		return builtin.Name, true
	}
	return "", false
}

func isSignedConstCastBuiltin(name string) bool {
	if signed, _, ok := ParseBitIntName(name); ok {
		return signed
	}
	switch name {
	case "char", "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}

func usesInt64ConstRange(name string) bool {
	if signed, width, ok := ParseBitIntName(name); ok {
		return signed && width == 64
	}
	switch name {
	case "char", "int", "isize", "i64":
		return true
	default:
		return false
	}
}

func signedConstCastRange(name string) (int64, int64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && signed {
		if width >= 64 {
			return math.MinInt64, math.MaxInt64, true
		}
		return -(int64(1) << (width - 1)), (int64(1) << (width - 1)) - 1, true
	}
	switch name {
	case "i8":
		return math.MinInt8, math.MaxInt8, true
	case "i16":
		return math.MinInt16, math.MaxInt16, true
	case "i32":
		return math.MinInt32, math.MaxInt32, true
	case "char", "int", "isize", "i64":
		return math.MinInt64, math.MaxInt64, true
	default:
		return 0, 0, false
	}
}

func unsignedConstCastMax(name string) (uint64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && !signed {
		if width >= 64 {
			return math.MaxInt64, true
		}
		return (uint64(1) << width) - 1, true
	}
	switch name {
	case "u8":
		return math.MaxUint8, true
	case "u16":
		return math.MaxUint16, true
	case "u32":
		return math.MaxUint32, true
	case "u64", "usize", "uintptr":
		return math.MaxInt64, true
	default:
		return 0, false
	}
}
