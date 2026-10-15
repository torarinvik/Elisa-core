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
}

// Options configures a solver subprocess.
type Options struct {
	// Binary is the solver executable (default "z3"). Any SMT-LIB2 solver works.
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
}

// Open starts a solver subprocess in incremental mode. A nil error means the process is live and
// ready for Check calls. The caller owns Close.
func Open(opts Options) (Solver, error) {
	binary := opts.Binary
	if binary == "" {
		binary = "z3"
	}
	args := opts.Args
	if len(args) == 0 && (binary == "z3" || strings.HasSuffix(binary, "/z3")) {
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

// CheckValues runs Check and, on Sat, fetches the model values for `vars` via (get-value ...). A
// fetch/parse failure degrades to an empty map (the proof verdict is unaffected — values are only a
// diagnostic). Any solver error inside the query batch makes the verdict Unknown: a query the solver
// did not fully accept proves nothing either way.
func (s *procSolver) CheckValues(assertions string, vars []string) (Result, map[string]string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkValuesLocked(assertions, vars)
}

// CheckBudget is Check under a one-off resource limit (z3 `:rlimit` units) instead of the
// session's; the session limit is restored afterwards. A non-positive budget is rejected (Unknown):
// it would mean "unlimited". Used for cheap side checks that must not inherit the full budget.
func (s *procSolver) CheckBudget(assertions string, rlimit int64) Result {
	if rlimit <= 0 {
		return Unknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return Unknown
	}
	if _, sawError, err := s.exchange(fmt.Sprintf("(set-option :rlimit %d)\n", rlimit)); err != nil || sawError {
		if err != nil {
			s.broken = true
		}
		return Unknown
	}
	res, _, _ := s.checkValuesLocked(assertions, nil)
	if _, sawError, err := s.exchange(fmt.Sprintf("(set-option :rlimit %d)\n", s.resourceLimit)); err != nil || sawError {
		// The session budget could not be restored: later verdicts would run under the wrong
		// limit, so retire the process rather than continue with a different contract.
		s.broken = true
	}
	return res
}

func (s *procSolver) checkValuesLocked(assertions string, vars []string) (Result, map[string]string, time.Duration) {
	s.stats.Queries++
	if s.broken {
		s.stats.Unknown++
		return Unknown, nil, 0
	}
	var b strings.Builder
	b.WriteString("(push 1)\n")
	b.WriteString(assertions)
	if !strings.HasSuffix(assertions, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("(check-sat)\n")
	if s.rlimitLog != nil {
		s.rlimitLast = s.readRlimit()
	}
	start := time.Now()
	lines, sawError, err := s.exchange(b.String())
	if err != nil {
		s.broken = true
		s.stats.Unknown++
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
	if s.rlimitLog != nil {
		checked := time.Since(start)
		verdict := "unknown"
		if res == Sat {
			verdict = "sat"
		} else if res == Unsat {
			verdict = "unsat"
		}
		fmt.Fprintf(s.rlimitLog, "%d %d %s\n", s.readRlimit()-s.rlimitLast, checked.Microseconds(), verdict)
	}
	values := map[string]string{}
	if res == Sat && len(vars) > 0 {
		// (get-value (v1 v2 ...)) → "((v1 5) (v2 3))" possibly across lines.
		if raw, verr, gerr := s.exchange("(get-value (" + strings.Join(vars, " ") + "))\n"); gerr != nil {
			s.broken = true
		} else if !verr {
			parseGetValue(strings.Join(raw, " "), values)
		}
	}
	if _, _, err := s.exchange("(pop 1)\n"); err != nil {
		s.broken = true
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
	lines, sawError, err := s.exchange("(get-info :rlimit)\n")
	if err != nil || sawError || len(lines) != 1 {
		return 0
	}
	var n int64
	if _, perr := fmt.Sscanf(strings.TrimSpace(lines[0]), "(:rlimit %d)", &n); perr != nil {
		return 0
	}
	return n
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

// parseGetValue extracts `name -> value` pairs from a (get-value ...) reply like
// "((v_a 5) (v_b (- 3)))". Tolerant: it scans for `(<name> <value>)` groups and stores the value
// text verbatim (negatives render as "(- 3)" → normalized to "-3").
func parseGetValue(raw string, out map[string]string) {
	s := strings.TrimSpace(raw)
	// Strip the single outer parens around the list of pairs.
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(strings.TrimSpace(s), ")")
	for {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			return
		}
		// Find the matching close for this pair.
		depth := 0
		end := -1
		for i := open; i < len(s); i++ {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			return
		}
		inner := strings.TrimSpace(s[open+1 : end])
		fields := strings.Fields(inner)
		if len(fields) >= 2 {
			name := fields[0]
			val := strings.Join(fields[1:], " ")
			val = strings.TrimSpace(val)
			// Normalize "(- 3)" → "-3".
			if strings.HasPrefix(val, "(-") {
				num := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(val, "(-"), ")"))
				val = "-" + num
			}
			out[name] = val
		}
		s = s[end+1:]
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
