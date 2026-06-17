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
