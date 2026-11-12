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
	"sync"
	"time"

	"elisacore/src/artifactidentity"
	"elisacore/src/proofcacheio"
	"elisacore/src/smt"
)

// This cache stores definitive UNSAT answers and model-free SAT answers to
// complete queries. It never stores timeout decisions. An alpha-renamed index
// reuses these verdicts after generated symbol locations move. Model-bearing
// declines and their counterexamples remain exact-text.
const smtProofCacheSchema = "elisa-exact-smt-proof-v1"

// identity binds decline records, whose counterexample text the analyzer formats.
// proofIdentity binds model-free records, which depend only on the query and
// on how the smt package runs the solver; an embedding compiler may key it on that
// package alone so analyzer changes keep the proofs.
type smtProofCache struct{ directory, identity, proofIdentity string }
type smtProofCacheRecord struct{ Schema, Key, Verdict, Checksum string }

type smtProofIdentityResolvers struct {
	compilerArtifact  func() (string, error)
	toolchainArtifact func(string) (string, error)
	solverArtifact    func() (string, error)
}

var (
	smtProofIdentityResolverMu sync.RWMutex
	smtProofIdentityResolver   smtProofIdentityResolvers
)

// SetSMTProofIdentityResolvers lets an embedding compiler share its already
// validated compiler and toolchain content stamps with the persistent proof
// cache. Standalone semantic users keep the local hashing fallback.
func SetSMTProofIdentityResolvers(compilerArtifact func() (string, error), toolchainArtifact func(string) (string, error)) {
	smtProofIdentityResolverMu.Lock()
	smtProofIdentityResolver.compilerArtifact = compilerArtifact
	smtProofIdentityResolver.toolchainArtifact = toolchainArtifact
	smtProofIdentityResolverMu.Unlock()
}

// SetSMTProofSolverIdentityResolver names the code that turns a query into a
// solver verdict (the smt package and its dependencies). Unsat records key on it
// instead of the whole compiler; without it they key on the compiler artifact.
func SetSMTProofSolverIdentityResolver(solverArtifact func() (string, error)) {
	smtProofIdentityResolverMu.Lock()
	smtProofIdentityResolver.solverArtifact = solverArtifact
	smtProofIdentityResolverMu.Unlock()
}

func currentSMTProofIdentityResolvers() smtProofIdentityResolvers {
	smtProofIdentityResolverMu.RLock()
	resolvers := smtProofIdentityResolver
	smtProofIdentityResolverMu.RUnlock()
	return resolvers
}

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
	if resolver := currentSMTProofIdentityResolvers().compilerArtifact; resolver != nil {
		return resolver()
	}
	return smtProofOpenedArtifactHash(smtProofCompilerArtifact, smtProofCompilerStat)
}

func smtProofOpenedArtifactHash(artifact *os.File, original os.FileInfo) (string, error) {
	return artifactidentity.OpenedHash(artifact, original)
}

func smtProofArtifactHash(path string) (string, error) {
	if resolver := currentSMTProofIdentityResolvers().toolchainArtifact; resolver != nil {
		return resolver(path)
	}
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
	version, ok := smtProofSolverVersion(solver)
	if !ok {
		return nil
	}
	proofHash := compilerHash
	if resolver := currentSMTProofIdentityResolvers().solverArtifact; resolver != nil {
		if stamp, err := resolver(); err == nil && stamp != "" {
			proofHash = stamp
		}
	}
	settings := fmt.Sprintf("rlimit=%d\x00timeout=120000\x00mbqi=true\x00models=true", smtResourceLimit())
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", smtProofCacheSchema, compilerHash, solverHash, version, runtime.GOOS, runtime.GOARCH, settings)
	proofIdentity := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", smtProofCacheSchema, proofHash, solverHash, version, runtime.GOOS, runtime.GOARCH, settings)
	return &smtProofCache{directory: directory, identity: identity, proofIdentity: proofIdentity}
}

// Every specialization that reaches the proof cache builds one of these, so the
// solver's version is remembered per file. A replaced solver has a new file
// identity or timestamp, which keeps a long-lived server's namespace current.
var (
	smtProofSolverVersionMu   sync.Mutex
	smtProofSolverVersionMemo = map[string]smtProofSolverVersionEntry{}
)

type smtProofSolverVersionEntry struct {
	info    os.FileInfo
	version string
}

func smtProofSolverVersion(solver string) (string, bool) {
	info, err := os.Stat(solver)
	if err != nil {
		return "", false
	}
	smtProofSolverVersionMu.Lock()
	defer smtProofSolverVersionMu.Unlock()
	if entry, ok := smtProofSolverVersionMemo[solver]; ok && os.SameFile(entry.info, info) && entry.info.Size() == info.Size() && entry.info.ModTime().Equal(info.ModTime()) {
		return entry.version, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var version smtProofVersionOutput
	command := exec.CommandContext(ctx, solver, "--version")
	command.WaitDelay = 500 * time.Millisecond
	command.Stdout = &version
	command.Stderr = io.Discard
	if command.Run() != nil {
		return "", false
	}
	text := version.String()
	smtProofSolverVersionMemo[solver] = smtProofSolverVersionEntry{info: info, version: text}
	return text, true
}

func (c *smtProofCache) key(query string) string {
	return smtProofCacheKey(c.proofID(), query)
}

func (c *smtProofCache) proofID() string {
	if c.proofIdentity != "" {
		return c.proofIdentity
	}
	return c.identity
}

func (c *smtProofCache) declineKey(query string, exact string) string {
	if c == nil || !smtDeclineCacheEnabled {
		return ""
	}
	if exact != "" && c.identity == c.proofID() {
		return exact
	}
	return smtProofCacheKey(c.identity, query)
}

func smtProofCacheKey(identity, query string) string {
	return proofcacheio.Key(identity, query)
}
func smtProofChecksum(key, verdict string) string {
	hash := sha256.Sum256([]byte(smtProofCacheSchema + "\x00" + key + "\x00" + verdict))
	return hex.EncodeToString(hash[:])
}
func (c *smtProofCache) path(key string) string {
	return filepath.Join(c.directory, key[:2], key+".json")
}

type smtProofKeys struct {
	exact   string
	alpha   string
	decline string
}

func (c *smtProofCache) contains(query string) bool {
	_, found := c.lookupProof(query)
	return found
}

// Return the calculated keys with a miss so a definitive proof can be
// published without hashing and normalizing the query a second time.
func (c *smtProofCache) lookupProof(query string) (smtProofKeys, bool) {
	if c == nil {
		return smtProofKeys{}, false
	}
	return c.lookupProofWithExact(query, c.key(query))
}

func (c *smtProofCache) lookupProofWithExact(query, exact string) (smtProofKeys, bool) {
	keys := smtProofKeys{exact: exact}
	if c.containsVerdict(keys.exact, "unsat") {
		return keys, true
	}
	keys.alpha, _ = c.alphaKey(query)
	return keys, keys.alpha != "" && c.containsVerdict(keys.alpha, "unsat")
}

func (c *smtProofCache) containsVerdict(key, verdict string) bool {
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
	return record.Schema == smtProofCacheSchema && record.Key == key && record.Verdict == verdict && record.Checksum == smtProofChecksum(key, verdict)
}
func (c *smtProofCache) publish(query string) {
	if c == nil {
		return
	}
	keys := smtProofKeys{exact: c.key(query)}
	keys.alpha, _ = c.alphaKey(query)
	c.publishProof(keys)
}

func (c *smtProofCache) publishProof(keys smtProofKeys) {
	if c == nil || keys.exact == "" {
		return
	}
	c.publishVerdict(keys.exact, "unsat")
	if keys.alpha != "" {
		c.publishVerdict(keys.alpha, "unsat")
	}
}
func (c *smtProofCache) publishVerdict(key, verdict string) {
	payload, err := json.Marshal(smtProofCacheRecord{smtProofCacheSchema, key, verdict, smtProofChecksum(key, verdict)})
	if err == nil {
		writeSMTCacheRecord(c.path(key), ".proof-*", payload)
	}
}

// Model-free SAT has no counterexample to relabel, so it can use the same
// solver identity and alpha-equivalence as a proof. Keep its domain separate
// from both UNSAT records and model-bearing declines.
func (keys smtProofKeys) modelFreeSATKey() string {
	if keys.alpha != "" {
		return smtProofCacheKey("model-free-sat-v1", keys.alpha)
	}
	return smtProofCacheKey("model-free-sat-v1", keys.exact)
}

func (c *smtProofCache) containsModelFreeSAT(keys smtProofKeys) bool {
	return c != nil && keys.exact != "" && c.containsVerdict(keys.modelFreeSATKey(), "sat")
}

func (c *smtProofCache) publishModelFreeSAT(keys smtProofKeys) {
	if c != nil && keys.exact != "" {
		c.publishVerdict(keys.modelFreeSATKey(), "sat")
	}
}

// Bound identity-probe output as well as its elapsed time.
type smtProofVersionOutput struct{ bytes.Buffer }

func (b *smtProofVersionOutput) Write(payload []byte) (int, error) {
	if b.Len()+len(payload) > 4096 {
		return 0, fmt.Errorf("solver version output exceeds limit")
	}
	return b.Buffer.Write(payload)
}

// Declines are kept under a separate schema. Only definitive SAT and explicitly
// certified independent resource exhaustion can be published. Generic Unknown
// (timeouts, transport/protocol errors, unsupported or incomplete solving) must
// remain retryable. Old uncertified unknown records are rejected by this schema.
const smtDeclineCacheSchema = "elisa-exact-smt-decline-v3"

// Definitive SAT and certified independent resource declines are cached
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
func (c *smtProofCache) declined(key string) (string, bool) {
	if key == "" || !smtDeclineCacheEnabled {
		return "", false
	}
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
	if record.Schema != smtDeclineCacheSchema || record.Key != key || (record.Verdict != "sat" && record.Verdict != "resource-unknown") ||
		record.Checksum != smtDeclineChecksum(key, record.Verdict, record.Counterexample) {
		return "", false
	}
	if record.Verdict == "resource-unknown" && !smtIndependent {
		return "", false
	}
	return record.Counterexample, true
}

func (c *smtProofCache) publishDeclined(key string, res smt.Result, counterexample string) {
	if res != smt.Sat {
		return
	}
	c.publishDeclineVerdict(key, "sat", counterexample)
}

// Call only after every Unknown in the independent compound check is certified.
func (c *smtProofCache) publishResourceDeclined(key string) {
	c.publishDeclineVerdict(key, "resource-unknown", "")
}

func (c *smtProofCache) publishDeclineVerdict(key string, verdict, counterexample string) {
	if key == "" || !smtDeclineCacheEnabled {
		return
	}
	payload, err := json.Marshal(smtDeclineCacheRecord{smtDeclineCacheSchema, key, verdict, counterexample, smtDeclineChecksum(key, verdict, counterexample)})
	if err == nil && len(payload) <= 1<<20 {
		writeSMTCacheRecord(c.declinePath(key), ".decline-*", payload)
	}
}

// Write to a sibling temporary file and publish with a rename. Existing
// shards need no directory probe, and successful rename consumes the temp.
func writeSMTCacheRecord(path, pattern string, payload []byte) {
	proofcacheio.WriteRecord(path, pattern, payload)
}
