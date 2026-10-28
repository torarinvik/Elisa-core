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

// Result is the outcome of a (check-sat) query.
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
	preamble                          string
	checkLimit                        int64
	warm                              bool // CheckAfterHistory: the next check continues the replayed session
	resourceUnknown, transientUnknown uint64
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

// certifyCanceled replays an ambiguously canceled independent query with the
// exact same resource budget and configured wall timer. It never treats elapsed
// wall time as evidence of resource failure. The before/after fuel delta
// excludes assertion parsing. Z3 clamps its counter
// to the scoped limit on exit, so exhaustion can equal (not exceed) the budget.
// A recovered SAT/UNSAT is definitive; a failed certification remains retryable.
func (s *procSolver) certifyCanceled(assertions string) (result Result, certified bool) {
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
	if s.rlimitLog != nil {
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
		if reason != `(:reason-unknown "canceled")` && reason != `(:reason-unknown "max. resource limit exceeded")` {
			return Unknown, false
		}
		if s.rlimitLog == nil {
			after, afterOK = s.resourceCount()
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
		checkLimit:    opts.ResourceLimit,
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

// Check implements Solver. It is safe for concurrent use (serialized — a single solver process has
// one command stream).
func (s *procSolver) Check(assertions string) (Result, time.Duration) {
	res, _, elapsed := s.CheckValues(assertions, nil)
	return res, elapsed
}

// exchange writes one command batch followed by a unique `(echo ...)` sentinel and reads every reply
// line up to that sentinel. Framing each batch keeps the command stream in lockstep: a solver
// `(error ...)` reply (an ill-sorted assertion, an undeclared symbol) is an EXTRA output line, and
// reading a fixed number of lines would leave the batch's real `sat`/`unsat` buffered as the answer
// to the NEXT query — an order-dependent verdict that could even turn a stale `unsat` into a false
// proof. It reports the reply lines and whether any of them was a solver error.
func (s *procSolver) exchange(batch string) ([]string, bool, error) {
	s.seq++
	sentinel := fmt.Sprintf("elisa-smt-frame-%d", s.seq)
	if _, err := io.WriteString(s.stdin, batch+"(echo \""+sentinel+"\")\n"); err != nil {
		return nil, false, err
	}
	var lines []string
	sawError := false
	for {
		line, err := s.out.ReadString('\n')
		t := strings.TrimSpace(line)
		if t == sentinel || t == "\""+sentinel+"\"" {
			return lines, sawError, nil
		}
		if t != "" {
			if strings.HasPrefix(t, "(error") {
				sawError = true
			}
			lines = append(lines, t)
		}
		if err != nil {
			return lines, sawError, err
		}
	}
}

// CheckValues runs Check and, on Sat, fetches the model values for `vars` via (get-value ...). If a
// requested model is missing or malformed, the result is Unknown so the caller cannot persist a
// decline with an incomplete counterexample. Any solver error inside the query batch likewise makes
// the verdict Unknown: a query the solver did not fully accept proves nothing either way.
func (s *procSolver) CheckValues(assertions string, vars []string) (Result, map[string]string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkValuesLocked(assertions, vars)
}

// CheckBudget is Check under a one-off resource limit (z3 `:rlimit` units) instead of the
// session's; the session limit is restored afterwards. A non-positive budget is rejected (Unknown):
// it would mean "unlimited". Used for cheap side checks that must not inherit the full budget.
func (s *procSolver) CheckBudget(assertions string, rlimit int64) Result {
	res, _ := s.CheckBudgetValues(assertions, nil, rlimit)
	return res
}

// CheckBudgetValues is CheckValues under a one-off resource limit (see CheckBudget).
func (s *procSolver) CheckBudgetValues(assertions string, vars []string, rlimit int64) (Result, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rlimit <= 0 {
		s.transientUnknown++
		return Unknown, nil
	}
	if s.broken {
		s.transientUnknown++
		return Unknown, nil
	}
	if s.independent {
		// Every check replays its own budget after the reset.
		s.checkLimit = rlimit
		res, values, _ := s.checkValuesLocked(assertions, vars)
		s.checkLimit = s.resourceLimit
		return res, values
	}
	if _, sawError, err := s.exchange(fmt.Sprintf("(set-option :rlimit %d)\n", rlimit)); err != nil || sawError {
		if err != nil {
			s.broken = true
		}
		s.transientUnknown++
		return Unknown, nil
	}
	res, values, _ := s.checkValuesLocked(assertions, vars)
	if _, sawError, err := s.exchange(fmt.Sprintf("(set-option :rlimit %d)\n", s.resourceLimit)); err != nil || sawError {
		// The session budget could not be restored: later verdicts would run under the wrong
		// limit, so retire the process rather than continue with a different contract.
		s.broken = true
	}
	return res, values
}

func (s *procSolver) checkValuesLocked(assertions string, vars []string) (Result, map[string]string, time.Duration) {
	s.stats.Queries++
	if s.broken {
		s.stats.Unknown++
		s.transientUnknown++
		return Unknown, nil, 0
	}
	var b strings.Builder
	if s.warm {
		// Continue the session CheckAfterHistory replayed, under this check's budget.
		b.WriteString("(push 1)\n")
		fmt.Fprintf(&b, "(set-option :rlimit %d)\n", s.checkLimit)
	} else if s.independent {
		b.WriteString("(reset)\n")
		b.WriteString(s.preamble)
		if s.checkLimit > 0 {
			fmt.Fprintf(&b, "(set-option :rlimit %d)\n", s.checkLimit)
		}
	} else {
		b.WriteString("(push 1)\n")
	}
	b.WriteString(assertions)
	if !strings.HasSuffix(assertions, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("(check-sat)\n")
	if s.rlimitLog != nil {
		// (reset) restarts z3's resource counter.
		s.rlimitLast = 0
		if !s.independent {
			s.rlimitLast = s.readRlimit()
		}
	}
	start := time.Now()
	lines, sawError, err := s.exchange(b.String())
	if err != nil {
		s.broken = true
		s.stats.Unknown++
		s.transientUnknown++
		return Unknown, nil, time.Since(start)
	}
	res := Unknown
	if !sawError && len(lines) == 1 {
		switch lines[0] {
		case "unsat":
			res = Unsat
		case "sat":
			res = Sat
		}
	}
	// Log the original verdict and interval before certification can reset the
	// solver or recover a different verdict. Reason/statistics probes are not
	// physical check time and must not be counted again in the replay row.
	if s.rlimitLog != nil {
		checked := time.Since(start)
		resources := s.readRlimit() - s.rlimitLast
		s.writePhysicalCheckLog(res, resources, checked, s.label)
	}
	if res == Unknown {
		certified := false
		if !sawError && len(lines) == 1 && lines[0] == "unknown" && s.independent && !s.warm && s.checkLimit > 0 {
			reason := s.unknownReason()
			if reason == `(:reason-unknown "max. resource limit exceeded")` {
				certified = true
			} else if reason == `(:reason-unknown "canceled")` {
				res, certified = s.certifyCanceled(assertions)
			}
		}
		if res == Unknown {
			if certified {
				s.resourceUnknown++
			} else {
				s.transientUnknown++
			}
		}
	}

	values := map[string]string{}
	if res == Sat && len(vars) > 0 {
		// (get-value (v1 v2 ...)) → "((v1 5) (v2 3))" possibly across lines.
		if raw, verr, gerr := s.exchange("(get-value (" + strings.Join(vars, " ") + "))\n"); gerr != nil {
			s.broken = true
			res = Unknown
			s.transientUnknown++
		} else if verr || !parseGetValue(strings.Join(raw, " "), values) || !modelHasEveryValue(values, vars) {
			res = Unknown
			s.transientUnknown++
			values = map[string]string{}
		}
	}
	if (!s.independent || s.warm) && !s.broken {
		if _, sawError, err := s.exchange("(pop 1)\n"); err != nil || sawError {
			s.transientUnknown++
			s.broken = true
		}
	}
	elapsed := time.Since(start)
	s.stats.Total += elapsed
	if elapsed > s.stats.Slowest {
		s.stats.Slowest = elapsed
	}
	switch res {
	case Unsat:
		s.stats.Unsat++
	case Sat:
		s.stats.Sat++
	default:
		s.stats.Unknown++
	}
	return res, values, elapsed
}

// readRlimit returns z3's cumulative resource counter (calibration logging only); 0 when unavailable.
func (s *procSolver) readRlimit() int64 {
	count, ok := s.resourceCount()
	if !ok || count > uint64(^uint64(0)>>1) {
		return 0
	}
	return int64(count)
}

// readBalancedSExpr reads solver output until parentheses balance — a (get-value ...) reply may span
// lines. Returns the accumulated text.
func (s *procSolver) readBalancedSExpr() (string, error) {
	var b strings.Builder
	depth := 0
	started := false
	for {
		line, err := s.out.ReadString('\n')
		b.WriteString(line)
		for _, c := range line {
			switch c {
			case '(':
				depth++
				started = true
			case ')':
				depth--
			}
		}
		if started && depth <= 0 {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), err
		}
	}
}

// parseGetValue extracts `name -> value` pairs from a complete `(get-value ...)` result. It accepts
// nested values such as bitvectors and negative integers, but rejects truncated pairs, duplicate
// names, and trailing malformed text instead of silently returning a partial counterexample.
func parseGetValue(raw string, out map[string]string) bool {
	pos := skipSMTWhitespace(raw, 0)
	if pos >= len(raw) || raw[pos] != '(' {
		return false
	}
	pos++
	for {
		pos = skipSMTWhitespace(raw, pos)
		if pos >= len(raw) {
			return false
		}
		if raw[pos] == ')' {
			pos++
			break
		}
		if raw[pos] != '(' {
			return false
		}
		pos++
		pos = skipSMTWhitespace(raw, pos)
		nameStart := pos
		nameEnd, ok := parseSMTAtom(raw, pos)
		if !ok {
			return false
		}
		name := raw[nameStart:nameEnd]
		pos = skipSMTWhitespace(raw, nameEnd)
		valueStart := pos
		valueEnd, ok := parseSMTExpression(raw, pos)
		if !ok {
			return false
		}
		value := strings.TrimSpace(raw[valueStart:valueEnd])
		if value == "" {
			return false
		}
		// Preserve the established diagnostic spelling for SMT's unary negative form.
		if strings.HasPrefix(value, "(-") {
			num := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "(-"), ")"))
			value = "-" + num
		}
		pos = skipSMTWhitespace(raw, valueEnd)
		if pos >= len(raw) || raw[pos] != ')' {
			return false
		}
		pos++
		if previous, duplicate := out[name]; duplicate {
			if previous != value {
				return false
			}
		} else {
			out[name] = value
		}
	}
	return skipSMTWhitespace(raw, pos) == len(raw)
}

func modelHasEveryValue(values map[string]string, vars []string) bool {
	wanted := make(map[string]struct{}, len(vars))
	for _, name := range vars {
		wanted[name] = struct{}{}
	}
	if len(values) != len(wanted) {
		return false
	}
	for name := range wanted {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}

func skipSMTWhitespace(text string, pos int) int {
	for pos < len(text) {
		switch text[pos] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// parseSMTAtom returns the end of one symbol atom (simple or quoted).
func parseSMTAtom(text string, pos int) (int, bool) {
	if pos >= len(text) || text[pos] == '(' || text[pos] == ')' || text[pos] == '"' {
		return pos, false
	}
	if text[pos] == '|' {
		for end := pos + 1; end < len(text); end++ {
			if text[end] == '|' {
				return end + 1, end > pos+1
			}
		}
		return pos, false
	}
	end := pos
	for end < len(text) {
		if text[end] == '(' || text[end] == ')' || text[end] == '"' || text[end] == '|' || text[end] == ';' || skipSMTWhitespace(text, end) != end {
			break
		}
		end++
	}
	return end, end > pos
}

// parseSMTExpression returns the end of one complete SMT-LIB s-expression.
func parseSMTExpression(text string, pos int) (int, bool) {
	if pos >= len(text) {
		return pos, false
	}
	switch text[pos] {
	case '(':
		pos++
		for {
			pos = skipSMTWhitespace(text, pos)
			if pos >= len(text) {
				return pos, false
			}
			if text[pos] == ')' {
				return pos + 1, true
			}
			end, ok := parseSMTExpression(text, pos)
			if !ok {
				return pos, false
			}
			pos = end
		}
	case ')', ';':
		return pos, false
	case '"':
		for end := pos + 1; end < len(text); {
			if text[end] == '"' {
				if end+1 < len(text) && text[end+1] == '"' {
					end += 2
					continue
				}
				return end + 1, true
			}
			end++
		}
		return pos, false
	case '|':
		return parseSMTAtom(text, pos)
	default:
		return parseSMTAtom(text, pos)
	}
}

// readNonEmptyLine reads the next non-blank output line from the solver.
func (s *procSolver) readNonEmptyLine() (string, error) {
	for {
		line, err := s.out.ReadString('\n')
		if t := strings.TrimSpace(line); t != "" {
			return t, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (s *procSolver) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *procSolver) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken || s.stdin == nil {
		_ = s.stdin.Close()
		return s.cmd.Wait()
	}
	_, _ = io.WriteString(s.stdin, "(exit)\n")
	_ = s.stdin.Close()
	return s.cmd.Wait()
}

// isZ3 reports whether binary names z3 (by basename, so a custom path still gets `-in`).
func isZ3(binary string) bool {
	base := binary[strings.LastIndexAny(binary, `/\`)+1:]
	base = strings.TrimSuffix(base, ".exe")
	return base == "z3"
}

// CheckAfterHistory decides assertions in a session that first checked each history entry in
// order, under the session budget, exactly as a push/pop session would have. Its verdict depends
// only on the history, the query and the budgets, so it is as reproducible as Check; it exists
// for queries whose proof relies on lemmas a warm session learns from earlier obligations.
// Only an independent solver supports it (Unknown otherwise).
func (s *procSolver) CheckAfterHistory(history []string, assertions string, vars []string, rlimit int64) (Result, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken || !s.independent || rlimit <= 0 {
		s.transientUnknown++
		return Unknown, nil
	}
	var b strings.Builder
	b.WriteString("(reset)\n")
	b.WriteString(s.preamble)
	if s.resourceLimit > 0 {
		fmt.Fprintf(&b, "(set-option :rlimit %d)\n", s.resourceLimit)
	}
	for _, entry := range history {
		b.WriteString("(push 1)\n")
		b.WriteString(entry)
		if !strings.HasSuffix(entry, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("(check-sat)\n(pop 1)\n")
	}
	if _, sawError, err := s.exchange(b.String()); err != nil || sawError {
		// Any failed history command leaves the replayed stack/state uncertain.
		// Retire this process instead of issuing the final query in a partial replay.
		s.broken = true
		s.transientUnknown++
		return Unknown, nil
	}
	// The outer verdict key does not include history, and replay does not
	// retain per-entry unknown reasons. Never memoize a replay decline, even
	// when its final check exhausts resources; proofs remain reusable.
	s.transientUnknown++

	s.warm, s.checkLimit = true, rlimit
	res, values, _ := s.checkValuesLocked(assertions, vars)
	s.warm, s.checkLimit = false, s.resourceLimit
	return res, values
}
