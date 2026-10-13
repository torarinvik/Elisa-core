package semantic

import "strings"

// Elisa builtin intrinsics: `@intrinsic("elisa.<name>")` externs with no single LLVM
// intrinsic, synthesized by the backend as internal always-inline functions.
var elisaBuiltinIntrinsics = map[string]bool{
	// High 64 bits of the full 128-bit unsigned product of two u64 (the low half is a * b).
	"elisa.mul_hi.u64": true,
	// Quotient of the 128-bit value (hi:lo) by d, truncated to 64 bits; exact when hi < d.
	// d must be non-zero (the caller checks).
	"elisa.div_wide.u64": true,
}

// IsElisaBuiltinIntrinsic reports whether name is a builtin the backend synthesizes.
func IsElisaBuiltinIntrinsic(name string) bool {
	return elisaBuiltinIntrinsics[strings.TrimSpace(name)]
}
