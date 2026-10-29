//go:build cgo

package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The reader may drop intrinsic progress attributes. The cache must restore
// the original attribute and prove the entire module is identical before reuse.
func TestOptimizedModuleDiskCacheRestoresIntrinsicProgress(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "submit_evidence.elisa", partitionSubmitGenericRuntimeSource())
	session := NewIncrementalObjectSession(2, 32<<20)
	session.EnableDiskCache(t.TempDir(), "roundtrip-regression", 64<<20)
	build := func() *llvmGenerator {
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.ensureTargetMachine(); err != nil {
			g.dispose()
			t.Fatal(err)
		}
		return g
	}
	first := build()
	defer first.dispose()
	cold, err := session.optimizeObjectModule(first, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	if cold != first {
		defer cold.dispose()
	}
	want := cold.printModule()
	decl := regexp.MustCompile(`(?m)^declare \{ i64, i1 \} @llvm\.sadd\.with\.overflow\.i64\([^\n]*\) #(\d+)$`).FindStringSubmatch(want)
	if len(decl) != 2 || !regexp.MustCompile(`(?m)^attributes #`+decl[1]+` = \{[^\n]*\bmustprogress\b`).MatchString(want) {
		t.Skip("LLVM did not retain the progress attribute on the overflow intrinsic")
	}
	if files, err := os.ReadDir(session.disk.root); err != nil || len(files) == 0 {
		t.Fatalf("exact restored module was not cached: %v", err)
	}
	second := build()
	defer second.dispose()
	replay, err := session.optimizeObjectModule(second, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	if replay != second {
		defer replay.dispose()
	}
	if got := replay.printModule(); got != want {
		t.Fatal("optimized cache replay changed the module")
	}
	if session.Stats().OptimizedModulesReused != 1 {
		t.Fatal("exact restored module was not reused")
	}
}

func TestOptimizedModuleEnvelopeRejectsMalformedOrLegacyData(t *testing.T) {
	digest := sha256.Sum256([]byte("original optimized IR"))
	bitcode := []byte("bitcode")
	encoded := encodeOptimizedModuleEnvelope(digest, bitcode)
	decoded, got, ok := decodeOptimizedModuleEnvelope(encoded)
	if !ok || got != digest || !bytes.Equal(decoded, bitcode) {
		t.Fatal("valid optimized module envelope changed its contents")
	}
	for _, data := range [][]byte{nil, bitcode, encoded[:len(optimizedModuleEnvelopeMagic)], encoded[:len(optimizedModuleEnvelopeMagic)+sha256.Size], append([]byte("x"), encoded...)} {
		if _, _, ok := decodeOptimizedModuleEnvelope(data); ok {
			t.Fatal("accepted malformed or legacy optimized module envelope")
		}
	}
}

// The disk entry checksum can be valid even if an old or faulty producer saved
// a mismatched original-IR digest. Every hit must compare the restored module
// to that authenticated digest before serving it.
func TestOptimizedModuleDiskCacheRejectsMismatchedIRDigest(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "optimized_digest.elisa", incrementalObjectFixture(32))
	session := NewIncrementalObjectSession(2, 32<<20)
	session.EnableDiskCache(t.TempDir(), "digest-regression", 64<<20)
	build := func() *llvmGenerator {
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel3, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { discardLLVMValueNames(g.context) })
		if err != nil {
			t.Fatal(err)
		}
		if err := g.ensureTargetMachine(); err != nil {
			g.dispose()
			t.Fatal(err)
		}
		return g
	}
	first := build()
	defer first.dispose()
	optimized, err := session.optimizeObjectModule(first, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	if optimized != first {
		defer optimized.dispose()
	}
	want := optimized.printModule()
	files, err := filepath.Glob(filepath.Join(session.disk.root, "*.part"))
	if err != nil || len(files) != 1 {
		t.Fatalf("safe fixture did not publish one cache artifact: %v %v", files, err)
	}
	entry, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(entry) < 64 {
		t.Fatal("truncated outer cache entry")
	}
	offset := 64 + binary.LittleEndian.Uint64(entry[8:16]) + binary.LittleEndian.Uint64(entry[16:24])
	if offset > uint64(len(entry)) {
		t.Fatal("invalid cache entry lengths")
	}
	payload := entry[int(offset):]
	if _, _, ok := decodeOptimizedModuleEnvelope(payload); !ok {
		t.Fatal("missing optimized module envelope")
	}
	payload[len(optimizedModuleEnvelopeMagic)] ^= 1
	digest := sha256.Sum256(payload)
	copy(entry[32:64], digest[:]) // Keep the outer checksum valid.
	if err := os.WriteFile(files[0], entry, 0600); err != nil {
		t.Fatal(err)
	}
	second := build()
	defer second.dispose()
	replayed, err := session.optimizeObjectModule(second, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != second {
		defer replayed.dispose()
	}
	if session.Stats().OptimizedModulesReused != 0 {
		t.Fatal("accepted a restored module with a mismatched original-IR digest")
	}
	if replayed.printModule() != want {
		t.Fatal("digest mismatch changed the cold rebuild")
	}
}

func TestOptimizedModuleMetadataCollisionDeclinesWithoutMutation(t *testing.T) {
	ir := validationPeelIR + "\n!elisacore.optimized_cache.mustprogress.v1 = !{!0}\n!0 = !{!\"occupied\"}\n"
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	before := g.printModule()
	if _, err := optimizedModulePreservedBitcodeRoundTripExact(g); err == nil {
		t.Fatal("cache writer accepted user metadata with its reserved name")
	}
	if got := g.printModule(); got != before {
		t.Fatal("declined cache writer changed the module")
	}
}
