package smt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type discardWriteCloser struct{ io.Writer }

func (discardWriteCloser) Close() error { return nil }

func TestUnknownCacheEpochRequiresExplicitResourceReason(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		resource    bool
	}{
		{"resource", `(:reason-unknown "max. resource limit exceeded")`, true},
		{"timeout", `(:reason-unknown "timeout")`, false},
		{"canceled", `(:reason-unknown "canceled")`, false},
		{"incomplete", `(:reason-unknown "incomplete")`, false},
		{"bad-reason", `(error "unsupported")`, false},
		{"malformed", `nonsense`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := "unknown\nelisa-smt-frame-1\n" + tc.reply + "\nelisa-smt-frame-2\nunsat\nelisa-smt-frame-3\n"
			if tc.name == "canceled" {
				replies = "unknown\nelisa-smt-frame-1\n" + tc.reply + "\nelisa-smt-frame-2\n(error \"uncertifiable replay\")\nelisa-smt-frame-3\nunsat\nelisa-smt-frame-4\n"
			}
			s := &procSolver{independent: true, checkLimit: 1000, stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(replies))}
			if result, _ := s.Check("(assert false)"); result != Unknown {
				t.Fatal(result)
			}
			resource, transient := s.UnknownCacheEpoch()
			if tc.resource {
				if resource != 1 || transient != 0 {
					t.Fatalf("uncertified resource: %d %d", resource, transient)
				}
			} else if resource != 0 || transient != 1 {
				t.Fatalf("transient certified: %d %d", resource, transient)
			}
			if result, _ := s.Check("(assert false)"); result != Unsat {
				t.Fatalf("retry lost framing: %v", result)
			}
		})
	}
}

func TestUnknownCacheEpochRejectsTransportAndErrorReplies(t *testing.T) {
	for _, reply := range []string{"", "(error \"bad assertion\")\nunsat\nelisa-smt-frame-1\n", "unknown\nelisa-smt-frame-1\n"} {
		s := &procSolver{independent: true, checkLimit: 1000, stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(reply))}
		if result, _ := s.Check("(assert false)"); result != Unknown {
			t.Fatal(result)
		}
		resource, transient := s.UnknownCacheEpoch()
		if resource != 0 || transient != 1 {
			t.Fatalf("failure certified: %d %d", resource, transient)
		}
	}
}

func TestSatModelRetrievalFailuresAreTransientUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, modelReply string
		valid            bool
	}{
		{"complete", "((x 7) (y (- 3)))\nelisa-smt-frame-2\n", true},
		{"missing-variable", "((x 7))\nelisa-smt-frame-2\n", false},
		{"malformed-pair", "((x 7) (y))\nelisa-smt-frame-2\n", false},
		{"trailing-garbage", "((x 7) (y 3)) trailing\nelisa-smt-frame-2\n", false},
		{"solver-error", "(error \"model unavailable\")\nelisa-smt-frame-2\n", false},
		{"transport-error", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := "sat\nelisa-smt-frame-1\n" + tc.modelReply
			s := &procSolver{independent: true, stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(replies))}
			result, model, _ := s.CheckValues("(assert true)", []string{"x", "y"})
			if tc.valid {
				if result != Sat || model["x"] != "7" || model["y"] != "-3" {
					t.Fatalf("complete model result=(%s, %v)", result, model)
				}
				resource, transient := s.UnknownCacheEpoch()
				if resource != 0 || transient != 0 {
					t.Fatalf("valid model changed unknown epoch: %d %d", resource, transient)
				}
				return
			}
			if result != Unknown || len(model) != 0 {
				t.Fatalf("incomplete model result=(%s, %v), want unknown with no values", result, model)
			}
			resource, transient := s.UnknownCacheEpoch()
			if resource != 0 || transient != 1 {
				t.Fatalf("model failure was not transient: %d %d", resource, transient)
			}
		})
	}
}

func TestCheckValuesAcceptsRepeatedRequestedSymbols(t *testing.T) {
	replies := "sat\nelisa-smt-frame-1\n((x 7) (x 7))\nelisa-smt-frame-2\n"
	s := &procSolver{independent: true, stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(replies))}
	result, model, _ := s.CheckValues("(assert true)", []string{"x", "x"})
	if result != Sat || model["x"] != "7" {
		t.Fatalf("duplicate request result=(%s, %v)", result, model)
	}
}

func TestIndependentResourceUnknownHasActualSolverCertificate(t *testing.T) {
	z3Available(t)
	solver, err := Open(Options{Independent: true, ResourceLimit: 1000, PerQueryTimeoutMillis: 120000})
	if err != nil {
		t.Fatal(err)
	}
	defer solver.Close()
	if result, _ := solver.Check(rlimitHardQuery); result != Unknown {
		t.Fatalf("hard query unexpectedly decided: %v", result)
	}
	resource, transient := solver.(interface{ UnknownCacheEpoch() (uint64, uint64) }).UnknownCacheEpoch()
	if resource != 1 || transient != 0 {
		t.Fatalf("resource exhaustion not certified: %d %d", resource, transient)
	}
	if result, _ := solver.Check("(declare-const x Int)(assert (not x))"); result != Unknown {
		t.Fatal("ill-sorted query accepted")
	}
	resource, transient = solver.(interface{ UnknownCacheEpoch() (uint64, uint64) }).UnknownCacheEpoch()
	if resource != 1 || transient != 1 {
		t.Fatalf("solver error certified: %d %d", resource, transient)
	}
	if result, _ := solver.Check("(assert false)"); result != Unsat {
		t.Fatalf("solver error prevented retry: %v", result)
	}
}

func TestCanceledReplayPreservesWallTimerAndFullRelativeFuel(t *testing.T) {
	for _, tc := range []struct {
		name, after, reason string
		certified           bool
	}{
		{"exhausted", "1057", `(:reason-unknown "canceled")`, true},
		{"before-budget", "1056", `(:reason-unknown "canceled")`, false},
		{"parse-cost-alone", "1000", `(:reason-unknown "canceled")`, false},
		{"counter-rollback", "56", `(:reason-unknown "canceled")`, false},
		{"missing-counter", "", `(:reason-unknown "canceled")`, false},
		{"timeout", "1057", `(:reason-unknown "timeout")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := "unknown\nelisa-smt-frame-1\n(:reason-unknown \"canceled\")\nelisa-smt-frame-2\nelisa-smt-frame-3\n(:rlimit-count 57)\nelisa-smt-frame-4\nunknown\nelisa-smt-frame-5\n" + tc.reason + "\nelisa-smt-frame-6\n"
			if tc.reason == `(:reason-unknown "canceled")` {
				replies += "(:rlimit-count " + tc.after + ")\nelisa-smt-frame-7\n"
			}
			var commands strings.Builder
			s := &procSolver{independent: true, checkLimit: 1000, preamble: "(set-option :timeout 120000)\n", stdin: discardWriteCloser{&commands}, out: bufio.NewReader(strings.NewReader(replies))}
			if result, _ := s.Check("(assert true)"); result != Unknown {
				t.Fatal(result)
			}
			resource, transient := s.UnknownCacheEpoch()
			if tc.certified {
				if resource != 1 || transient != 0 {
					t.Fatalf("missed certificate %d %d", resource, transient)
				}
			} else if resource != 0 || transient != 1 {
				t.Fatalf("spurious certificate %d %d", resource, transient)
			}
			text := commands.String()
			if strings.Count(text, "(set-option :timeout 120000)") < 2 || strings.Contains(text, "(set-option :timeout 0)") || !strings.Contains(text, "(set-option :rlimit 1000)") {
				t.Fatal("configured wall timer or exact replay budget lost")
			}
		})
	}
}

func TestCanceledReplayRecoversDefinitiveVerdictAndModel(t *testing.T) {
	for _, verdict := range []string{"sat", "unsat"} {
		replies := "unknown\nelisa-smt-frame-1\n(:reason-unknown \"canceled\")\nelisa-smt-frame-2\nelisa-smt-frame-3\n(:rlimit-count 57)\nelisa-smt-frame-4\n" + verdict + "\nelisa-smt-frame-5\n"
		if verdict == "sat" {
			replies += "((x 7))\nelisa-smt-frame-6\n"
		}
		s := &procSolver{independent: true, checkLimit: 1000, stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(replies))}
		result, model, _ := s.CheckValues("(declare-const x Int)(assert (= x 7))", []string{"x"})
		if result.String() != verdict || verdict == "sat" && model["x"] != "7" {
			t.Fatalf("recovery lost result/model: %v %v", result, model)
		}
		resource, transient := s.UnknownCacheEpoch()
		if resource != 0 || transient != 0 {
			t.Fatal("definitive recovery classified as unknown")
		}
	}
}

func TestResourceCounterRejectsMalformedOrAmbiguousReplies(t *testing.T) {
	for _, reply := range []string{"nonsense :rlimit-count 1057", "(:rlimit-count 1057) extra", "(:rlimit-count 1057 :rlimit-count 1058)", "(:rlimit-count -1)", "(:rlimit-count 18446744073709551616)", "(:rlimit-count)", "((:rlimit-count 1057))"} {
		s := &procSolver{stdin: discardWriteCloser{io.Discard}, out: bufio.NewReader(strings.NewReader(reply + "\nelisa-smt-frame-1\n"))}
		if _, ok := s.resourceCount(); ok {
			t.Fatalf("malformed counter accepted: %q", reply)
		}
	}
}

type timedCheckWriter struct{ calls int }

func (w *timedCheckWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "(check-sat)") {
		w.calls++
		time.Sleep(8 * time.Millisecond)
	}
	return len(p), nil
}
func (*timedCheckWriter) Close() error { return nil }

func TestResourceCertificationLogsEachPhysicalCheckOnce(t *testing.T) {
	replies := "unknown\nelisa-smt-frame-1\n(:rlimit-count 1057)\nelisa-smt-frame-2\n(:reason-unknown \"canceled\")\nelisa-smt-frame-3\nelisa-smt-frame-4\n(:rlimit-count 57)\nelisa-smt-frame-5\nunknown\nelisa-smt-frame-6\n(:rlimit-count 1057)\nelisa-smt-frame-7\n(:reason-unknown \"canceled\")\nelisa-smt-frame-8\n"
	writer := &timedCheckWriter{}
	var log strings.Builder
	s := &procSolver{independent: true, checkLimit: 1000, stdin: writer, out: bufio.NewReader(strings.NewReader(replies)), rlimitLog: &log, label: "fixture:1 | work | query | goal"}
	if result, _ := s.Check("(assert true)"); result != Unknown {
		t.Fatal(result)
	}
	rows := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(rows) != 2 || writer.calls != 2 || s.Stats().Queries != 2 {
		t.Fatalf("physical checks not logged once: checks=%d stats=%+v log=%q", writer.calls, s.Stats(), log.String())
	}
	var resourceSum, microSum int64
	for i, row := range rows {
		header, label, ok := strings.Cut(row, "\t")
		if !ok {
			t.Fatal("missing attribution")
		}
		var resources, micros int64
		var verdict string
		if _, err := fmt.Sscanf(header, "%d %d %s", &resources, &micros, &verdict); err != nil {
			t.Fatal(err)
		}
		want := int64(1057)
		if resources != want || verdict != "unknown" || micros < 7000 {
			t.Fatalf("wrong physical row %d: %s", i, row)
		}
		tagged := strings.Contains(label, "resource-certify")
		if tagged != (i == 1) {
			t.Fatalf("misattributed certification row: %s", row)
		}
		if i == 1 && !strings.Contains(strings.SplitN(label, " | ", 4)[2], "resource-certify") {
			t.Fatal("certification tag lost kind attribution")
		}
		resourceSum += resources
		microSum += micros
	}
	if resourceSum != 2114 {
		t.Fatalf("resource accounting duplicated: %d", resourceSum)
	}
	if microSum > s.Stats().Total.Microseconds() {
		t.Fatalf("physical wall time counted twice: rows=%d total=%v", microSum, s.Stats().Total)
	}
}

func TestOriginalPhysicalLogSurvivesFailedCertification(t *testing.T) {
	replies := "unknown\nelisa-smt-frame-1\n(:rlimit-count 1057)\nelisa-smt-frame-2\n(:reason-unknown \"canceled\")\nelisa-smt-frame-3\n(error \"replay setup failed\")\nelisa-smt-frame-4\n"
	writer := &timedCheckWriter{}
	var log strings.Builder
	s := &procSolver{independent: true, checkLimit: 1000, stdin: writer, out: bufio.NewReader(strings.NewReader(replies)), rlimitLog: &log, label: "original"}
	if result, _ := s.Check("(assert true)"); result != Unknown {
		t.Fatal(result)
	}
	rows := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(rows) != 1 || writer.calls != 1 || !strings.HasPrefix(rows[0], "1057 ") || !strings.HasSuffix(rows[0], "\toriginal") {
		t.Fatalf("original row lost after failed reset/setup: %q", log.String())
	}
	resource, transient := s.UnknownCacheEpoch()
	if resource != 0 || transient != 1 {
		t.Fatal("failed certification became cacheable")
	}
}

func TestFailedPhysicalCertificationStillLogsBothAttempts(t *testing.T) {
	replies := "unknown\nelisa-smt-frame-1\n(:rlimit-count 1057)\nelisa-smt-frame-2\n(:reason-unknown \"canceled\")\nelisa-smt-frame-3\nelisa-smt-frame-4\n(:rlimit-count 57)\nelisa-smt-frame-5\n(error \"check failed\")\nelisa-smt-frame-6\n"
	writer := &timedCheckWriter{}
	var log strings.Builder
	s := &procSolver{independent: true, checkLimit: 1000, stdin: writer, out: bufio.NewReader(strings.NewReader(replies)), rlimitLog: &log, label: "original"}
	if result, _ := s.Check("(assert true)"); result != Unknown {
		t.Fatal(result)
	}
	rows := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(rows) != 2 || writer.calls != 2 || !strings.HasPrefix(rows[0], "1057 ") || !strings.HasPrefix(rows[1], "0 ") || !strings.Contains(rows[1], "resource-certify") {
		t.Fatalf("failed physical attempt not attributed: %q", log.String())
	}
	resource, transient := s.UnknownCacheEpoch()
	if resource != 0 || transient != 1 {
		t.Fatal("failed physical certification became cacheable")
	}
}

// A warm replay has a history-dependent negative result; even an explicit
// resource-limit reason cannot certify it under a query-only cache key.
type respondingWriteCloser func([]byte) (int, error)

func (f respondingWriteCloser) Write(p []byte) (int, error) { return f(p) }
func (respondingWriteCloser) Close() error                  { return nil }

func TestWarmExplicitResourceReasonRemainsUncertified(t *testing.T) {
	var replies bytes.Buffer
	var commands strings.Builder
	writer := respondingWriteCloser(func(p []byte) (int, error) {
		text := string(p)
		commands.WriteString(text)
		if strings.Contains(text, "(check-sat)") {
			replies.WriteString("unknown\n")
		}
		if strings.Contains(text, "(get-info :reason-unknown)") {
			replies.WriteString("(:reason-unknown \"max. resource limit exceeded\")\n")
		}
		echo := strings.LastIndex(text, "(echo \"")
		if echo < 0 {
			t.Fatal("unframed command")
		}
		frame, _, _ := strings.Cut(text[echo+7:], "\"")
		fmt.Fprintln(&replies, frame)
		return len(p), nil
	})
	s := &procSolver{independent: true, warm: true, checkLimit: 1000,
		stdin: writer, out: bufio.NewReader(&replies)}
	if result, _ := s.Check("(assert true)"); result != Unknown {
		t.Fatal(result)
	}
	resource, transient := s.UnknownCacheEpoch()
	if resource != 0 || transient != 1 {
		t.Fatalf("warm decline certified: resource=%d transient=%d", resource, transient)
	}
	if strings.Contains(commands.String(), "reason-unknown") || strings.Contains(commands.String(), "(reset)") {
		t.Fatalf("warm check attempted independent certification: %q", commands.String())
	}
}
