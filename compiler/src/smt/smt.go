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
	}
	s.stats.SpawnMillis = time.Since(start)
	// Print success/error tokens so we can read a deterministic line per command, and keep models
	// available for counterexample extraction by the caller (cheap; only fetched on Sat).
	preamble := "(set-option :print-success false)\n(set-option :produce-models true)\n"
	if opts.PerQueryTimeoutMillis > 0 {
		preamble += fmt.Sprintf("(set-option :timeout %d)\n", opts.PerQueryTimeoutMillis)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Queries++
	if s.broken {
		s.stats.Unknown++
		return Unknown, 0
	}
	// Bracket the obligation's assertions in their own scope so shared base declarations (asserted
	// once, outside any push) persist while each obligation's scratch state is discarded.
	var b strings.Builder
	b.WriteString("(push 1)\n")
	b.WriteString(assertions)
	if !strings.HasSuffix(assertions, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("(check-sat)\n")
	b.WriteString("(pop 1)\n")

	start := time.Now()
	if _, err := io.WriteString(s.stdin, b.String()); err != nil {
		s.broken = true
		s.stats.Unknown++
		return Unknown, 0
	}
	line, err := s.readNonEmptyLine()
	elapsed := time.Since(start)
	if err != nil {
		s.broken = true
		s.stats.Unknown++
		return Unknown, elapsed
	}
	s.stats.Total += elapsed
	if elapsed > s.stats.Slowest {
		s.stats.Slowest = elapsed
	}
	switch strings.TrimSpace(line) {
	case "unsat":
		s.stats.Unsat++
		return Unsat, elapsed
	case "sat":
		s.stats.Sat++
		return Sat, elapsed
	default: // "unknown" or anything unexpected
		s.stats.Unknown++
		return Unknown, elapsed
	}
}

// CheckValues runs Check and, on Sat, fetches the model values for `vars` via (get-value ...). A
// fetch/parse failure degrades to an empty map (the proof verdict is unaffected — values are only a
// diagnostic). Implemented as Check followed, while the scope is still on the stack, by get-value —
// so it brackets its own push/pop and the value query sees the same assertions.
func (s *procSolver) CheckValues(assertions string, vars []string) (Result, map[string]string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	start := time.Now()
	if _, err := io.WriteString(s.stdin, b.String()); err != nil {
		s.broken = true
		s.stats.Unknown++
		return Unknown, nil, 0
	}
	line, err := s.readNonEmptyLine()
	if err != nil {
		s.broken = true
		s.stats.Unknown++
		return Unknown, nil, time.Since(start)
	}
	res := Unknown
	switch strings.TrimSpace(line) {
	case "unsat":
		res = Unsat
	case "sat":
		res = Sat
	}
	values := map[string]string{}
	if res == Sat && len(vars) > 0 {
		// (get-value (v1 v2 ...)) → "((v1 5) (v2 3))" possibly across lines.
		_, _ = io.WriteString(s.stdin, "(get-value ("+strings.Join(vars, " ")+"))\n")
		if raw, rerr := s.readBalancedSExpr(); rerr == nil {
			parseGetValue(raw, values)
		}
	}
	if _, err := io.WriteString(s.stdin, "(pop 1)\n"); err != nil {
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
