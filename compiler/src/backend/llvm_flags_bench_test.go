//go:build cgo

package backend

import "testing"

const flagsMembershipBenchmarkSource = `struct Flags[T]:
    bits: u64

const enum RoutineFlag of u8:
    External
    Forward
    VarArgs
    Export
    Inline
    Builtin

def routine_allows_direct_call(flags: Flags[RoutineFlag]&) -> bool:
    return flags[RoutineFlag.External] or flags[RoutineFlag.Forward] or flags[RoutineFlag.Builtin]

def routine_needs_wrapper(flags: Flags[RoutineFlag]&) -> bool:
    return flags[RoutineFlag.VarArgs] or flags[RoutineFlag.Export] or flags[RoutineFlag.Inline]
`

func BenchmarkGenerateLLVMIRFlagsMembership(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "flags_membership_bench.elisa", flagsMembershipBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}
