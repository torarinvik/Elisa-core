package backend

import (
	"os"
	"strings"
)

// TargetCPUEnv carries the `-mcpu=<name>` CLI choice to code generation. The build is
// in-process, so the driver sets it once (like ELISACORE_FAST_MATH / ELISACORE_STRICT_FP)
// and every target machine the backend creates reads it.
const TargetCPUEnv = "ELISACORE_TARGET_CPU"

// RequestedTargetCPU returns the normalized `-mcpu` request: "" (compiler default: host
// CPU for native builds, generic for an explicit -target-triple), "native", or an LLVM CPU
// name such as "apple-m1", "znver4" or "generic".
func RequestedTargetCPU() string {
	return NormalizeTargetCPU(os.Getenv(TargetCPUEnv))
}

// NormalizeTargetCPU canonicalizes a `-mcpu` value. `host` is accepted as an alias of
// `native` (LLVM's spelling).
func NormalizeTargetCPU(value string) string {
	v := strings.TrimSpace(value)
	if strings.EqualFold(v, "native") || strings.EqualFold(v, "host") {
		return "native"
	}
	return v
}

// LTOMode selects link-time optimization for native executables and `-emit obj`.
type LTOMode string

const (
	LTOOff  LTOMode = ""
	LTOFull LTOMode = "full"
	LTOThin LTOMode = "thin"
)

// ParseLTOMode accepts the `-flto[=...]` / project `"lto"` spellings.
func ParseLTOMode(value string) (LTOMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "none", "false", "no":
		return LTOOff, true
	case "full", "fat", "true", "yes", "on":
		return LTOFull, true
	case "thin":
		return LTOThin, true
	default:
		return LTOOff, false
	}
}

// TargetIdentity contains resolved LLVM target-machine inputs, not just requests.
type TargetIdentity struct {
	Triple   string
	CPU      string
	Features string
}
