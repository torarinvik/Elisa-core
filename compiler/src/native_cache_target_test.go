package main

import (
	"crypto/sha256"
	"elisacore/src/backend"
	"testing"
)

func TestTestRunnerCacheKeysCodegenEnvironment(t *testing.T) {
	for _, name := range []string{"ELISACORE_TARGET_CPU", "ELISACORE_FAST_MATH", "ELISACORE_STRICT_FP", "ELISACORE_NOALIAS_MUTABLE_REFS", "ELISACORE_FORCE_BOUNDS_CHECK", "ELISA_LINEAR_MUTATION"} {
		t.Run(name, func(t *testing.T) {
			key := func() string {
				h := sha256.New()
				if err := writeCommonTestRunnerCacheInputs(h, nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), ""); err != nil {
					t.Fatal(err)
				}
				return string(h.Sum(nil))
			}
			t.Setenv(name, "0")
			before := key()
			t.Setenv(name, "1")
			after := key()
			if before == after {
				t.Fatal("changed configuration reused test runner identity")
			}
		})
	}
}

func TestNativeCacheResolvedTargetFields(t *testing.T) {
	key := func(identity backend.TargetIdentity) string {
		h := sha256.New()
		writeResolvedNativeCacheTarget(h, identity)
		return string(h.Sum(nil))
	}
	base := backend.TargetIdentity{Triple: "aarch64-unknown-linux-gnu", CPU: "generic", Features: "+neon"}
	for _, changed := range []backend.TargetIdentity{
		{Triple: "aarch64-apple-darwin", CPU: base.CPU, Features: base.Features},
		{Triple: base.Triple, CPU: "apple-m1", Features: base.Features},
		{Triple: base.Triple, CPU: base.CPU, Features: "+neon,+sve"},
	} {
		if key(base) == key(changed) {
			t.Fatalf("resolved target change collided: %+v", changed)
		}
	}
	if key(base) != key(base) {
		t.Fatal("unstable target identity")
	}
}
