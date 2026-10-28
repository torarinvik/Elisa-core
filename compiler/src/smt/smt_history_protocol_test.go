package smt

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCheckAfterHistoryRejectsMalformedReplayReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  string
		checks int
		want   Result
	}{
		{"missing", "", 1, Unknown},
		{"extra", "sat\nunsat\n", 1, Unknown},
		{"malformed", "not-a-verdict\n", 1, Unknown},
		{"sat", "sat\n", 1, Unsat},
		{"unsat", "unsat\n", 1, Unsat},
		{"unknown", "unknown\n", 1, Unsat},
		{"zero", "", 0, Unsat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var replies bytes.Buffer
			var commands strings.Builder
			writer := respondingWriteCloser(func(p []byte) (int, error) {
				text := string(p)
				commands.WriteString(text)
				if strings.Contains(text, "history_marker") {
					replies.WriteString(tc.reply)
				} else if strings.Contains(text, "(assert false)") {
					replies.WriteString("unsat\n")
				}
				echo := strings.LastIndex(text, "(echo \"")
				if echo < 0 {
					t.Fatal("unframed command")
				}
				frame, _, _ := strings.Cut(text[echo+7:], "\"")
				fmt.Fprintln(&replies, frame)
				return len(p), nil
			})
			s := &procSolver{independent: true, checkLimit: 1000, resourceLimit: 2000, stdin: writer, out: bufio.NewReader(&replies)}
			history := make([]string, tc.checks)
			for i := range history {
				history[i] = "; history_marker\n(assert true)"
			}
			got, _ := s.CheckAfterHistory(history, "(assert false)", nil, 1000)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if got == Unknown && (!s.broken || strings.Contains(commands.String(), "(assert false)")) {
				t.Fatal("malformed replay was not retired before final query")
			}
		})
	}
}

func TestCheckAfterHistoryRealSolverProtocol(t *testing.T) {
	z3Available(t)
	solver, err := Open(Options{Independent: true, ResourceLimit: 6000000})
	if err != nil {
		t.Fatal(err)
	}
	defer solver.Close()
	proc := solver.(*procSolver)
	result, _ := proc.CheckAfterHistory([]string{"(assert true)", "(assert false)"}, "(assert false)", nil, 6000000)
	if result != Unsat {
		t.Fatalf("expected unsat after valid history, got %v", result)
	}
}
