package main

import (
	"os/exec"
	"testing"

	"elisacore/src/backend"
)

func TestRuntimeObjectCacheKeysCodegenEnvironment(t *testing.T) {
	if clang, err := exec.LookPath("clang"); err == nil {
		if _, err := toolchainNativeContentStamp(clang); err != nil {
			t.Skipf("clang selector is not cacheable: %v", err)
		}
	}
	for _, name := range []string{"ELISACORE_TARGET_CPU", "ELISACORE_FAST_MATH", "ELISACORE_STRICT_FP", "ELISACORE_NOALIAS_MUTABLE_REFS", "ELISACORE_FORCE_BOUNDS_CHECK", "ELISA_LINEAR_MUTATION"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "0")
			before, err := runtimeObjectCacheArtifactFor(backend.DefaultPackedLoweringProfile(), "")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(name, "1")
			after, err := runtimeObjectCacheArtifactFor(backend.DefaultPackedLoweringProfile(), "")
			if err != nil {
				t.Fatal(err)
			}
			if before.key == after.key || before.object == after.object {
				t.Fatal("changed codegen environment reused runtime object identity")
			}
		})
	}
}

// The runtime-object cache key must be stable for identical inputs and must change when any
// input that affects the emitted object changes. Key incompleteness is the only real
// correctness risk (a stale object served for changed inputs), so pin the sensitivity here.
func TestRuntimeObjectCacheKeySensitivity(t *testing.T) {
	t.Parallel()

	base, err := runtimeObjectCacheArtifactFor(backend.DefaultPackedLoweringProfile(), "")
	if err != nil {
		t.Skipf("cannot derive runtime-object cache key (toolchain/runtime unavailable): %v", err)
	}
	if base.key == "" {
		t.Fatal("expected a non-empty cache key")
	}

	// Stable: same inputs -> same key.
	again, err := runtimeObjectCacheArtifactFor(backend.DefaultPackedLoweringProfile(), "")
	if err != nil {
		t.Fatalf("re-derive key: %v", err)
	}
	if again.key != base.key {
		t.Fatalf("key not stable for identical inputs: %s != %s", again.key, base.key)
	}

	// Sensitive: a different target triple must change the key (object is triple-specific).
	triple, err := runtimeObjectCacheArtifactFor(backend.DefaultPackedLoweringProfile(), "x86_64-unknown-linux-gnu")
	if err != nil {
		t.Fatalf("derive key for alternate triple: %v", err)
	}
	if triple.key == base.key {
		t.Fatal("expected a different cache key for a different target triple")
	}

	// The artifact path must live under the key directory so distinct keys never collide.
	if base.object == triple.object {
		t.Fatalf("distinct keys produced the same object path: %s", base.object)
	}
}
