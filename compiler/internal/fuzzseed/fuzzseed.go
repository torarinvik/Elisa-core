// Package fuzzseed supplies seed inputs and shared guards for the native `go test -fuzz`
// targets over the stage0 front end (lexer, parser, checker).
//
// Seeds are gathered at fuzz start from the module itself, so the corpus tracks the
// language as it changes without a committed snapshot: every *.elisa file under the
// module root, plus the Go raw-string literals in *_test.go files that look like Elisa
// programs (most front-end regression tests embed their source that way).
package fuzzseed

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxInput bounds fuzz inputs. Throughput matters more than giant programs: every
// crash found so far reproduces in a few hundred bytes.
const MaxInput = 8 << 10

var (
	seedOnce sync.Once
	seeds    [][]byte
)

var rawStringRE = regexp.MustCompile("(?s)`([^`]*)`")

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func looksLikeElisa(s string) bool {
	if len(s) < 8 || len(s) > MaxInput {
		return false
	}
	for _, kw := range []string{"def ", "struct ", "enum ", "const ", "machine "} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// Seeds returns the deduplicated, sorted seed corpus (deterministic order).
func Seeds() [][]byte {
	seedOnce.Do(func() {
		root := moduleRoot()
		set := map[string]bool{}
		if root != "" {
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					name := d.Name()
					if name == ".git" || name == "node_modules" || (name == "testdata" && strings.Contains(path, string(filepath.Separator)+"fuzz")) {
						return filepath.SkipDir
					}
					return nil
				}
				switch {
				case strings.HasSuffix(path, ".elisa"):
					data, err := os.ReadFile(path)
					if err == nil && len(data) <= MaxInput {
						set[string(data)] = true
					}
				case strings.HasSuffix(path, "_test.go"):
					data, err := os.ReadFile(path)
					if err != nil {
						return nil
					}
					for _, m := range rawStringRE.FindAllStringSubmatch(string(data), -1) {
						if looksLikeElisa(m[1]) {
							set[m[1]] = true
						}
					}
				}
				return nil
			})
		}
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			seeds = append(seeds, []byte(k))
		}
	})
	return seeds
}

// Watchdog fails loudly when one input runs longer than limit, dumping every goroutine
// so the hung frame is visible. Keep limit under ~10s: the go fuzz coordinator kills a
// worker that stops responding for about that long, silently, and the stack is lost.
// Call the returned stop function when the input finishes.
func Watchdog(limit time.Duration, label string, input []byte) func() {
	timer := time.AfterFunc(limit, func() {
		buf := make([]byte, 1<<22)
		n := runtime.Stack(buf, true)
		report := fmt.Sprintf("fuzzseed: %s exceeded %s on %d-byte input %q\n%s\n", label, limit, len(input), input, buf[:n])
		fmt.Fprint(os.Stderr, report)
		recordEvent("hang-"+label, report)
		os.Exit(3)
	})
	return func() { timer.Stop() }
}

// recordEvent appends a report to $ELISA_FUZZ_EVENT_DIR (when set). The go fuzz
// coordinator does not always surface a dying worker's stderr — a hang caught while
// minimizing shows only "terminated unexpectedly" — so the evidence goes to a file too.
func recordEvent(kind, report string) {
	dir := os.Getenv("ELISA_FUZZ_EVENT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	name := filepath.Join(dir, fmt.Sprintf("%s-%d-%d.txt", kind, os.Getpid(), time.Now().UnixNano()))
	_ = os.WriteFile(name, []byte(report), 0o644)
}

// LimitStack caps the goroutine stack so unbounded recursion on deeply nested input
// fails fast as a reproducible "goroutine stack exceeds" fatal error instead of
// growing toward the 1 GB default in every fuzz worker at once.
func LimitStack() {
	stackOnce.Do(func() { debug.SetMaxStack(256 << 20) })
}

var stackOnce sync.Once

// NoteInput records the input a worker is about to run in $ELISA_FUZZ_EVENT_DIR/last-<pid>
// (when set). A fatal runtime error (stack overflow, out of memory) kills the worker
// without a recoverable panic, and when that happens during minimization the go fuzz
// coordinator saves a different, often harmless, input. The dead worker's last-<pid>
// file is then the real crasher.
func NoteInput(input []byte) {
	dir := os.Getenv("ELISA_FUZZ_EVENT_DIR")
	if dir == "" {
		return
	}
	noteOnce.Do(func() {
		_ = os.MkdirAll(dir, 0o755)
		notePath = filepath.Join(dir, fmt.Sprintf("last-%d", os.Getpid()))
	})
	_ = os.WriteFile(notePath, input, 0o644)
}

var (
	noteOnce sync.Once
	notePath string
)
