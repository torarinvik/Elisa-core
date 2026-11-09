package smt

import (
	"fmt"
	"io"
	"strings"
	"time"
)

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

// CheckAlternateRelevancy retries an independent Z3 query with a different search
// strategy. The next independent check resets the session and its options.
func (s *procSolver) CheckAlternateRelevancy(assertions string, vars []string, rlimit int64) (Result, map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.independent || !s.z3 || s.broken || rlimit <= 0 {
		return Unknown, nil, false
	}
	s.checkLimit = rlimit
	result, values, _ := s.checkValuesLocked("(set-option :smt.relevancy 1)\n"+assertions, vars)
	s.checkLimit = s.resourceLimit
	return result, values, true
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
	var lines []string
	var sawError bool
	var err error
	var warmBefore, warmAfter uint64
	var warmBeforeOK, warmAfterOK bool
	if s.warm && s.warmHistoryCertified {
		setup := strings.TrimSuffix(b.String(), "(check-sat)\n")
		var setupLines []string
		setupLines, sawError, err = s.exchange(setup)
		if err == nil && !sawError && len(setupLines) == 0 {
			warmBefore, warmBeforeOK = s.resourceCount()
			lines, sawError, err = s.exchange("(check-sat)\n")
			warmAfter, warmAfterOK = s.resourceCount()
		} else if err == nil {
			sawError = true
		}
	} else {
		lines, sawError, err = s.exchange(b.String())
	}
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
		snapshotCertified := false
		if !sawError && len(lines) == 1 && lines[0] == "unknown" && s.warm && s.warmHistoryCertified && s.checkLimit > 0 && warmBeforeOK && warmAfterOK && warmAfter >= warmBefore && warmAfter-warmBefore >= uint64(s.checkLimit) {
			reason := s.unknownReason()
			snapshotCertified = isAmbiguousCancellation(reason) || reason == `(:reason-unknown "max. resource limit exceeded")`
		}
		if !sawError && len(lines) == 1 && lines[0] == "unknown" && s.independent && !s.warm && s.checkLimit > 0 {
			reason := s.unknownReason()
			if reason == `(:reason-unknown "max. resource limit exceeded")` {
				certified = true
			} else if isAmbiguousCancellation(reason) {
				originalTotal, originalOK := s.resourceCount()
				res, certified = s.certifyCanceledFrom(assertions, originalTotal, originalOK)
			}
		}
		if res == Unknown {
			if snapshotCertified {
				s.snapshotUnknown++
			} else if certified {
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
