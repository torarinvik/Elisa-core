package main

import (
	"crypto/sha256"
	"elisacore/src/backend"
	"testing"
)

func TestTestRunnerCacheKeysCodegenEnvironment(t *testing.T) {
	for _, name := range []string{"ELISACORE_TARGET_CPU", "ELISACORE_FAST_MATH", "ELISACORE_STRICT_FP", "ELISACORE_NOALIAS_MUTABLE_REFS", "ELISACORE_FORCE_BOUNDS_CHECK", "ELISACORE_NO_LINK_BINDING_CHECK", "ELISA_LINEAR_MUTATION"} {
		t.Run(name, func(t *testing.T) {
			key := func() string {
				h := sha256.New()
				if err := writeCommonTestRunnerCacheInputs(h, nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false); err != nil {
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

func TestNativeCacheKeysToolchainEnvironment(t *testing.T) {
	for _, name := range nativeToolEnvironmentInputs {
		t.Run(name, func(t *testing.T) {
			key := func() string {
				t.Helper()
				h := sha256.New()
				writeNativeToolEnvironmentInputs(h)
				return string(h.Sum(nil))
			}
			t.Setenv(name, "first-value")
			before := key()
			t.Setenv(name, "second-value")
			if before == key() {
				t.Fatalf("changed native tool environment %s reused a cache identity", name)
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

func TestNativeCacheCodegenWorkerCountIsKeyed(t *testing.T) {
	key := func() string {
		t.Helper()
		h := sha256.New()
		if err := writeNativeCacheTarget(h, ""); err != nil {
			t.Fatal(err)
		}
		return string(h.Sum(nil))
	}
	t.Setenv("ELISACORE_CODEGEN_JOBS", "1")
	serial := key()
	t.Setenv("ELISACORE_CODEGEN_JOBS", "2")
	parallel := key()
	if serial == parallel {
		t.Fatal("different resolved codegen worker counts reused a native cache identity")
	}
}
