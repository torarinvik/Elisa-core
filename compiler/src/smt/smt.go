// Package smt is a thin, solver-agnostic harness over an SMT-LIB2 solver subprocess (docs/90).
//
// It is the foundation of the optional SMT discharge tier: the bounded-linear prover (docs/86)
// handles the common cases cheaply, and an obligation it declines — a non-linear product, a
// division, a quantified property — is handed here as SMT-LIB2 text. The solver runs in incremental
// mode (a single long-lived process), so we pay the spawn cost once and amortize it across every
// query in a compilation.
//
// Design choices (docs/90 §2):
//   - SMT-LIB2 over stdin/stdout, NOT CGO bindings: zero native build dependency, and any solver that
//     speaks SMT-LIB2 (z3, cvc5, yices) is a drop-in via the binary path. If profiling later shows
//     IPC dominates, an in-process backend can implement the same Solver interface.
//   - Every query is timed; the caller aggregates the numbers into the --explain profile so "is SMT
//     cheap or demanding?" is answered with data, not guesses.
//   - Fail-safe: any I/O error, a missing solver, or a malformed answer surfaces as Unknown, never a
//     false Sat/Unsat. The discharge tier treats Unknown as "decline" (fall back to a runtime check),
//     so a broken solver can only cost optimization, never soundness.
package smt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Result int

const (
	// Unknown: the solver could not decide (timeout, incompleteness) or an error occurred. The
	// discharge tier MUST treat this as "decline" — never as a proof either way.
	Unknown Result = iota
	// Sat: the assertions are satisfiable. When checking the NEGATION of an obligation, Sat means a
	// counterexample exists → the obligation is refuted.
	Sat
	// Unsat: the assertions are unsatisfiable. When checking the negation of an obligation, Unsat
	// means the obligation holds for all inputs → proven.
	Unsat
)

func (r Result) String() string {
	switch r {
	case Sat:
		return "sat"
	case Unsat:
		return "unsat"
	default:
		return "unknown"
	}
}

// Stats accumulates harness telemetry for the profile report.
type Stats struct {
	Queries     int           // number of (check-sat) calls
	Sat         int           // resolved satisfiable
	Unsat       int           // resolved unsatisfiable
	Unknown     int           // undecided / errored
	Total       time.Duration // wall time spent inside the solver across all queries
	Slowest     time.Duration // slowest single query
	SpawnMillis time.Duration // one-time process-start cost
}

// Solver is the minimal incremental SMT interface the discharge tier needs. A Push/…/Pop pair
// brackets one obligation's scratch assertions so the base context (shared declarations) survives.
type Solver interface {
	// Check asserts `assertions` inside a fresh scope, runs (check-sat), pops the scope, and returns
	// the result with the time the solver itself took. The body is raw SMT-LIB2 (declarations +
	// asserts); the harness wraps it in push/pop and check-sat.
	Check(assertions string) (Result, time.Duration)
	// CheckValues is Check plus, on Sat, the solver's values for `vars` (an empty map otherwise) —
	// the raw material for a counterexample diagnostic. Names are the SMT symbols as declared in
	// `assertions`.
	CheckValues(assertions string, vars []string) (Result, map[string]string, time.Duration)
	Stats() Stats
	Close() error
}

// procSolver drives a solver binary in incremental mode over stdin/stdout.
type procSolver struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stats  Stats
	broken bool // once the pipe breaks, every further Check is Unknown
	seq    int  // per-batch sentinel counter for exchange framing
	// rlimitLog, when non-nil (ELISA_SMT_RLIMIT_LOG), receives one "delta elapsed_us verdict" line per
	// check-sat: the resource units it consumed. Calibration only; off by default.
	rlimitLog  io.Writer
	rlimitLast int64
	// resourceLimit is the session `:rlimit` (0 = none); CheckBudget restores it.
	resourceLimit int64
	// label is appended (tab-separated) to rlimit log lines; calibration attribution only.
	label string
	// independent mode (Options.Independent): preamble is replayed after each (reset), under
	// checkLimit, the budget of the check in progress.
	independent                       bool
	z3                                bool
	preamble                          string
	checkLimit                        int64
	warm                              bool // CheckAfterHistory: the next check continues the replayed session
	warmHistoryCertified              bool
	resourceUnknown, transientUnknown uint64
	snapshotUnknown                   uint64
	// Speculative-check helpers (prefetch.go). opts re-opens a helper with this
	// solver's configuration; hasOpts is false for hand-built test solvers.
	opts             Options
	hasOpts          bool
	closed           bool
	idleHelpers      []*procSolver
	helperCount      int
	prefetchInFlight sync.WaitGroup
}

// SnapshotUnknownEpoch counts resource-bounded declines that depend on the
// complete replay history. They are safe only in a snapshot containing that
// history, never under a query-only persistent proof-cache key.
func (s *procSolver) SnapshotUnknownEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotUnknown
}

// UnknownCacheEpoch reports monotonic counts of resource-certified and other
// Unknown checks. Compound callers compare epochs so a later resource failure
// cannot conceal an earlier timeout, protocol error or broken transport.
func (s *procSolver) UnknownCacheEpoch() (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourceUnknown, s.transientUnknown
}

// unknownReason returns a reason only for a clean, single framed reply.
func (s *procSolver) unknownReason() string {
	lines, sawError, err := s.exchange("(get-info :reason-unknown)\n")
	if err != nil {
		s.broken = true
	}
	if err != nil || sawError || len(lines) != 1 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

// resourceCount reads a snapshot; querying statistics performs no solver search.
// Resource limits are relative to the counter at check-sat, not reset: parsing
// assertions has already used units, so an absolute counter is not a certificate.
func (s *procSolver) resourceCount() (uint64, bool) {
	lines, sawError, err := s.exchange("(get-info :all-statistics)\n")
	if err != nil {
		s.broken = true
	}
	if err != nil || sawError {
		return 0, false
	}
	raw := strings.TrimSpace(strings.Join(lines, " "))
	if len(raw) < 2 || raw[0] != '(' || raw[len(raw)-1] != ')' {
		return 0, false
	}
	body := raw[1 : len(raw)-1]
	if strings.ContainsAny(body, "()") {
		return 0, false
	}
	fields := strings.Fields(body)
	if len(fields)%2 != 0 {
		return 0, false
	}
	var count uint64
	found := false
	for i := 0; i < len(fields); i += 2 {
		if len(fields[i]) < 2 || fields[i][0] != ':' {
			return 0, false
		}
		if fields[i] != ":rlimit-count" {
			continue
		}
		if found {
			return 0, false
		}
		value, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return 0, false
		}
		count, found = value, true
	}
	return count, found
}

// isAmbiguousCancellation recognizes exact Z3 cancellation reports. Neither
// string alone certifies resource exhaustion: only a replay with a full fuel
// delta may do so.
func isAmbiguousCancellation(reason string) bool {
	return reason == `(:reason-unknown "canceled")` || reason == `(:reason-unknown "push canceled")`
}

// certifyCanceled replays an ambiguously canceled independent query with the
// exact same resource budget and configured wall timer. It never treats elapsed
// wall time as evidence of resource failure. The before/after fuel delta
// excludes assertion parsing. Z3 clamps its counter
// to the scoped limit on exit, so exhaustion can equal (not exceed) the budget.
// A recovered SAT/UNSAT is definitive; a failed certification remains retryable.
func (s *procSolver) certifyCanceled(assertions string) (result Result, certified bool) {
	return s.certifyCanceledFrom(assertions, 0, false)
}

// certifyCanceledFrom is certifyCanceled given the ORIGINAL check's post-check fuel total (since
// its reset, parsing included). The replayed setup is deterministic, so its fuel is exactly the
// original's parse cost: when the original's total minus that parse cost already reaches the
// budget, the original check is certified without replaying the (expensive) search itself.
func (s *procSolver) certifyCanceledFrom(assertions string, originalTotal uint64, originalOK bool) (result Result, certified bool) {
	if !s.independent || s.warm || s.checkLimit <= 0 || s.broken {
		return Unknown, false
	}
	setup := "(reset)\n" + s.preamble + assertions
	if !strings.HasSuffix(setup, "\n") {
		setup += "\n"
	}
	setup += fmt.Sprintf("(set-option :rlimit %d)\n", s.checkLimit)
	if lines, sawError, err := s.exchange(setup); err != nil || sawError || len(lines) != 0 {
		if err != nil {
			s.broken = true
		}
		return Unknown, false
	}
	before, ok := s.resourceCount()
	if !ok {
		return Unknown, false
	}
	if originalOK && originalTotal >= before && originalTotal-before >= uint64(s.checkLimit) {
		s.stats.Unknown++ // the original canceled check, certified by its own measured fuel
		return Unknown, true
	}
	var checked time.Duration
	var resources int64
	defer func() {
		label := "resource-certify"
		if s.label != "" {
			parts := strings.SplitN(s.label, " | ", 4)
			if len(parts) == 4 {
				parts[2] += " [resource-certify]"
				label = strings.Join(parts, " | ")
			} else {
				label = s.label + " [resource-certify]"
			}
		}
		s.writePhysicalCheckLog(result, resources, checked, label)
	}()
	s.stats.Unknown++ // original canceled check; the replay is accounted below
	s.stats.Queries++
	started := time.Now()
	lines, sawError, err := s.exchange("(check-sat)\n")
	checked = time.Since(started)
	if err != nil {
		s.broken = true
	}
	if err != nil || sawError || len(lines) != 1 {
		return Unknown, false
	}
	var after uint64
	var afterOK bool
	if lines[0] == "unknown" || s.rlimitLog != nil {
		// Snapshot before asking for the reason: a solver may charge that probe.
		after, afterOK = s.resourceCount()
		if afterOK && after <= uint64(^uint64(0)>>1) {
			// Independent physical checks start at reset, like ordinary log rows.
			// Certification below separately excludes assertion parsing.
			resources = int64(after)
		}
	}
	switch lines[0] {
	case "sat":
		return Sat, false
	case "unsat":
		return Unsat, false
	case "unknown":
		reason := s.unknownReason()
		if !isAmbiguousCancellation(reason) && reason != `(:reason-unknown "max. resource limit exceeded")` {
			return Unknown, false
		}
		return Unknown, afterOK && after >= before && after-before >= uint64(s.checkLimit)
	}
	return Unknown, false
}

// writePhysicalCheckLog emits exactly one already-measured physical check. It
// never queries the solver, so attribution and elapsed time survive later resets
// or a failed certification attempt.
func (s *procSolver) writePhysicalCheckLog(result Result, resources int64, checked time.Duration, label string) {
	if s.rlimitLog == nil {
		return
	}
	if label != "" {
		fmt.Fprintf(s.rlimitLog, "%d %d %s\t%s\n", resources, checked.Microseconds(), result.String(), label)
	} else {
		fmt.Fprintf(s.rlimitLog, "%d %d %s\n", resources, checked.Microseconds(), result.String())
	}
}

// LogsRlimit reports whether ELISA_SMT_RLIMIT_LOG calibration logging is active, so callers
// build attribution labels only when they will be written.
func (s *procSolver) LogsRlimit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rlimitLog != nil
}

// SetLogLabel sets the attribution label for subsequent rlimit log lines (tabs and newlines
// are replaced by spaces). It never affects any verdict.
func (s *procSolver) SetLogLabel(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(label)
}

// DefaultBinary is the solver used when Options.Binary is empty and ELISA_Z3 is unset. Builds
// set it with `-ldflags "-X elisacore/src/smt.DefaultBinary=/path/to/z3"` (the Makefile's Z3
// variable), so a toolchain built against its own Z3 finds it without PATH changes.
var DefaultBinary = "z3"

// ResolveBinary picks the solver executable: an explicit binary, else $ELISA_Z3, else
// DefaultBinary.
func ResolveBinary(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("ELISA_Z3"); env != "" {
		return env
	}
	return DefaultBinary
}

// Options configures a solver subprocess.
type Options struct {
	// Binary is the solver executable (default: ResolveBinary). Any SMT-LIB2 solver works.
	Binary string
	// Args are extra flags. Defaults supply incremental stdin mode for z3.
	Args []string
	// PerQueryTimeoutMillis bounds a single (check-sat). 0 = solver default. Keeps a pathological
	// obligation from stalling the compile; a timeout surfaces as Unknown → runtime fallback.
	PerQueryTimeoutMillis int
	// ResourceLimit bounds a single (check-sat) in z3 resource units (`:rlimit`). Unlike a wall-clock
	// timeout it is DETERMINISTIC: the same query gets the same verdict regardless of machine load.
	// 0 = unlimited. Exhaustion surfaces as Unknown, exactly like a timeout.
	ResourceLimit int64
	// Independent resets the solver before every (check-sat) instead of bracketing it in
	// push/pop, so a verdict depends only on the query, its budget and the solver build, never
	// on earlier queries in the session. Measured on the self-host's 3956 queries: identical to
	// a fresh process per query, in either order.
	Independent bool
}

// Open starts a solver subprocess in incremental mode. A nil error means the process is live and
// ready for Check calls. The caller owns Close.
func Open(opts Options) (Solver, error) {
	binary := ResolveBinary(opts.Binary)
	args := opts.Args
	if len(args) == 0 && isZ3(binary) {
		// z3 incremental mode: read SMT-LIB2 commands from stdin.
		args = []string{"-in"}
	}
	start := time.Now()
	cmd := exec.Command(binary, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("smt: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("smt: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("smt: start %q: %w", binary, err)
	}
	s := &procSolver{
		cmd:   cmd,
		stdin: stdin,
		out:   bufio.NewReader(stdout),

		resourceLimit: opts.ResourceLimit,
		independent:   opts.Independent,
		z3:            isZ3(binary),
		checkLimit:    opts.ResourceLimit,
		opts:          opts,
		hasOpts:       true,
	}
	s.stats.SpawnMillis = time.Since(start)
	// Print success/error tokens so we can read a deterministic line per command, and keep models
	// available for counterexample extraction by the caller (cheap; only fetched on Sat).
	preamble := "(set-option :print-success false)\n(set-option :produce-models true)\n"
	// Force model-based quantifier instantiation ON (it is z3's default, but make it explicit so an
	// emitted `:pattern` trigger can never render a genuinely-true quantified goal unprovable). MBQI is
	// the completeness fallback: when E-matching on a trigger fails to find an instantiation, MBQI still
	// searches for one, so trigger choice only affects solver SPEED, never which goals are provable.
	preamble += "(set-option :smt.mbqi true)\n"
	if opts.PerQueryTimeoutMillis > 0 {
		preamble += fmt.Sprintf("(set-option :timeout %d)\n", opts.PerQueryTimeoutMillis)
	}
	s.preamble = preamble
	if opts.ResourceLimit > 0 {
		preamble += fmt.Sprintf("(set-option :rlimit %d)\n", opts.ResourceLimit)
	}
	if path := os.Getenv("ELISA_SMT_RLIMIT_LOG"); path != "" {
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); ferr == nil {
			s.rlimitLog = f
		}
	}
	if _, err := io.WriteString(stdin, preamble); err != nil {
		s.broken = true
		return s, fmt.Errorf("smt: write preamble: %w", err)
	}
	return s, nil
}
