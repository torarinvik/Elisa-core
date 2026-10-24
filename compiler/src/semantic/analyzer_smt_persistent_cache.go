package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"elisacore/src/smt"
)

// This cache stores only definitive UNSAT answers to complete queries. It does
// not authorize skipping semantic analysis, and never stores timeout decisions.
// A separate alpha-renamed index reuses definitive proofs after generated symbol
// locations move; declined verdicts and their counterexamples remain exact-text.
const smtProofCacheSchema = "elisa-exact-smt-proof-v1"

type smtProofCache struct{ directory, identity string }
type smtProofCacheRecord struct{ Schema, Key, Verdict, Checksum string }

// Keep the compiler artifact opened from process startup. Renaming a new
// compiler over its pathname must not identify already-running code as that new build.
var smtProofCompilerArtifact, smtProofCompilerStat = func() (*os.File, os.FileInfo) {
	path, err := os.Executable()
	if err != nil {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil
	}
	return f, info
}()

func smtProofCompilerHash() (string, error) {
	return smtProofOpenedArtifactHash(smtProofCompilerArtifact, smtProofCompilerStat)
}

func smtProofOpenedArtifactHash(artifact *os.File, original os.FileInfo) (string, error) {
	if artifact == nil || original == nil {
		return "", fmt.Errorf("compiler identity unavailable")
	}
	before, err := artifact.Stat()
	if err != nil {
		return "", err
	}
	unchanged := func(info os.FileInfo) bool {
		return info.Size() == original.Size() && info.ModTime().Equal(original.ModTime())
	}
	if !unchanged(before) {
		return "", fmt.Errorf("running compiler artifact changed")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(artifact, 0, before.Size())); err != nil {
		return "", err
	}
	after, err := artifact.Stat()
	if err != nil || !unchanged(after) {
		return "", fmt.Errorf("compiler changed during identity read")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func smtProofArtifactHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func newSMTProofCache(directory, binary string) *smtProofCache {
	if directory == "" {
		return nil
	}
	solver, err := exec.LookPath(smt.ResolveBinary(binary))
	if err != nil {
		return nil
	}
	// Re-read identities for each analysis so a long-lived compile server cannot
	// keep an old cache namespace after its solver has been replaced.
	compilerHash, err := smtProofCompilerHash()
	if err != nil {
		return nil
	}
	solverHash, err := smtProofArtifactHash(solver)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var version smtProofVersionOutput
	command := exec.CommandContext(ctx, solver, "--version")
	command.WaitDelay = 500 * time.Millisecond
	command.Stdout = &version
	command.Stderr = io.Discard
	if command.Run() != nil {
		return nil
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", smtProofCacheSchema, compilerHash, solverHash, version.String(), runtime.GOOS, runtime.GOARCH)

	return &smtProofCache{directory: directory, identity: fmt.Sprintf("%s\x00rlimit=%d\x00timeout=120000\x00mbqi=true\x00models=true", identity, smtResourceLimit())}
}

func (c *smtProofCache) key(query string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(c.identity))
	_, _ = hash.Write([]byte{0})
	if len(query) <= 4096 {
		_, _ = hash.Write([]byte(query))
	} else {
		// The digest's interface makes []byte(query) escape. Bound scratch
		// instead of allocating another complete copy of every large query.
		var block [4096]byte
		for len(query) != 0 {
			n := copy(block[:], query)
			_, _ = hash.Write(block[:n])
			query = query[n:]
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}
func smtProofChecksum(key string) string {
	hash := sha256.Sum256([]byte(smtProofCacheSchema + "\x00" + key + "\x00unsat"))
	return hex.EncodeToString(hash[:])
}
func (c *smtProofCache) path(key string) string {
	return filepath.Join(c.directory, key[:2], key+".json")
}
func (c *smtProofCache) contains(query string) bool {
	if c == nil {
		return false
	}
	if c.containsKey(c.key(query)) {
		return true
	}
	if key, ok := c.alphaKey(query); ok {
		return c.containsKey(key)
	}
	return false
}
func (c *smtProofCache) containsKey(key string) bool {
	f, err := os.Open(c.path(key))
	if err != nil {
		return false
	}
	defer f.Close()
	payload, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(payload) > 4096 {
		return false
	}
	var record smtProofCacheRecord
	if json.Unmarshal(payload, &record) != nil {
		return false
	}
	return record.Schema == smtProofCacheSchema && record.Key == key && record.Verdict == "unsat" && record.Checksum == smtProofChecksum(key)
}
func (c *smtProofCache) publish(query string) {
	if c == nil {
		return
	}
	c.publishKey(c.key(query))
	if key, ok := c.alphaKey(query); ok {
		c.publishKey(key)
	}
}
func (c *smtProofCache) publishKey(key string) {
	path := c.path(key)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	payload, err := json.Marshal(smtProofCacheRecord{smtProofCacheSchema, key, "unsat", smtProofChecksum(key)})
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".proof-*")
	if err != nil {
		return
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(payload); err != nil {
		f.Close()
		return
	}
	if err = f.Close(); err != nil {
		return
	}
	_ = os.Rename(temporary, path)
}

// Bound identity-probe output as well as its elapsed time.
type smtProofVersionOutput struct{ bytes.Buffer }

func (b *smtProofVersionOutput) Write(payload []byte) (int, error) {
	if b.Len()+len(payload) > 4096 {
		return 0, fmt.Errorf("solver version output exceeds limit")
	}
	return b.Buffer.Write(payload)
}

// Declined verdicts (sat with its counterexample text, or unknown) are cached only on explicit
// opt-in (ELISACORE_SMT_CACHE_DECLINED=1) and under their own schema. A declined verdict never
// proves anything, so reusing one cannot accept a program; it can only keep declining an
// obligation that a fresh solver run might still decide. Identical query text can already get
// different declined verdicts across runs (solver history in the shared push/pop process), so
// this trades that run-to-run variation for speed on unchanged obligations.
const smtDeclineCacheSchema = "elisa-exact-smt-decline-v1"

// An independent solver's declined verdicts are as reproducible as its proofs, so they are cached
// by default; ELISACORE_SMT_CACHE_DECLINED=0 turns that off.
var smtDeclineCacheEnabled = os.Getenv("ELISACORE_SMT_CACHE_DECLINED") == "1" || smtIndependent && os.Getenv("ELISACORE_SMT_CACHE_DECLINED") != "0"

type smtDeclineCacheRecord struct{ Schema, Key, Verdict, Counterexample, Checksum string }

func smtDeclineChecksum(key, verdict, counterexample string) string {
	hash := sha256.Sum256([]byte(smtDeclineCacheSchema + "\x00" + key + "\x00" + verdict + "\x00" + counterexample))
	return hex.EncodeToString(hash[:])
}

func (c *smtProofCache) declinePath(key string) string {
	return filepath.Join(c.directory, "declined", key[:2], key+".json")
}

// declined returns a cached declined verdict's counterexample text.
func (c *smtProofCache) declined(query string) (string, bool) {
	if c == nil || !smtDeclineCacheEnabled {
		return "", false
	}
	key := c.key(query)
	f, err := os.Open(c.declinePath(key))
	if err != nil {
		return "", false
	}
	defer f.Close()
	payload, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(payload) > 1<<20 {
		return "", false
	}
	var record smtDeclineCacheRecord
	if json.Unmarshal(payload, &record) != nil {
		return "", false
	}
	if record.Schema != smtDeclineCacheSchema || record.Key != key || (record.Verdict != "sat" && record.Verdict != "unknown") ||
		record.Checksum != smtDeclineChecksum(key, record.Verdict, record.Counterexample) {
		return "", false
	}
	return record.Counterexample, true
}

func (c *smtProofCache) publishDeclined(query string, res smt.Result, counterexample string) {
	if c == nil || !smtDeclineCacheEnabled || res == smt.Unsat {
		return
	}
	verdict := "unknown"
	if res == smt.Sat {
		verdict = "sat"
	}
	key := c.key(query)
	path := c.declinePath(key)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	payload, err := json.Marshal(smtDeclineCacheRecord{smtDeclineCacheSchema, key, verdict, counterexample, smtDeclineChecksum(key, verdict, counterexample)})
	if err != nil || len(payload) > 1<<20 {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".decline-*")
	if err != nil {
		return
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(payload); err != nil {
		f.Close()
		return
	}
	if err = f.Close(); err != nil {
		return
	}
	_ = os.Rename(temporary, path)
}
