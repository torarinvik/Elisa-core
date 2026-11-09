package smt

import (
	"fmt"
	"io"
	"strings"
)

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
	s.closeHelpers()
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

// validHistoryReplies enforces one well-formed verdict per replayed check. The
// reset and option commands have print-success disabled and emit no replies.
func validHistoryReplies(lines []string, checks int) bool {
	if len(lines) != checks {
		return false
	}
	for _, line := range lines {
		if line != "sat" && line != "unsat" && line != "unknown" {
			return false
		}
	}
	return true
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
	lines, sawError, err := s.exchange(b.String())
	historyCertified := err == nil && !sawError && len(lines) == 0 && s.resourceLimit > 0
	if len(lines) != 0 {
		sawError = true
	}
	for _, entry := range history {
		if err != nil || sawError {
			break
		}
		if !strings.HasSuffix(entry, "\n") {
			entry += "\n"
		}
		lines, sawError, err = s.exchange("(push 1)\n" + entry)
		if err != nil || sawError || len(lines) != 0 {
			break
		}
		before, beforeOK := s.resourceCount()
		lines, sawError, err = s.exchange("(check-sat)\n")
		if err != nil || sawError || !validHistoryReplies(lines, 1) {
			sawError = true
			break
		}
		if lines[0] == "unknown" {
			after, afterOK := s.resourceCount()
			reason := s.unknownReason()
			if !beforeOK || !afterOK || after < before || after-before < uint64(s.resourceLimit) || (!isAmbiguousCancellation(reason) && reason != `(:reason-unknown "max. resource limit exceeded")`) {
				historyCertified = false
			}
		}
		lines, sawError, err = s.exchange("(pop 1)\n")
		if err != nil || sawError || len(lines) != 0 {
			break
		}
	}
	if err != nil || sawError || len(lines) != 0 {
		// Any failed history command leaves the replayed stack/state uncertain.
		// Retire this process instead of issuing the final query in a partial replay.
		s.broken = true
		s.transientUnknown++
		return Unknown, nil
	}
	// The outer verdict key does not include history, and replay does not
	// retain per-entry unknown reasons. Never memoize a replay decline, even
	// when its final check exhausts resources; proofs remain reusable.
	if !historyCertified {
		s.transientUnknown++
	}

	s.warm, s.warmHistoryCertified, s.checkLimit = true, historyCertified, rlimit
	res, values, _ := s.checkValuesLocked(assertions, vars)
	s.warm, s.warmHistoryCertified, s.checkLimit = false, false, s.resourceLimit
	return res, values
}
