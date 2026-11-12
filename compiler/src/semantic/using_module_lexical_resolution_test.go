package semantic

import (
	"strings"
	"testing"
)

func TestUsingResolvesNearestModule(t *testing.T) {
	cases := []struct{ name, source string }{
		{"root wildcard", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library
    def run() -> i64: return value()
`},
		{"duplicate selective same resolved target", `module Library:
    public:
        def value() -> i64: return 1
using Library::value
module Client:
    using Library::value
    def run() -> i64: return value()
`},
		{"root selective", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library::value
    def run() -> i64: return value()
`},
		{"nested shadows root", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    module Library:
        public:
            def inner() -> i64: return 2
    using Library
    def run() -> i64: return inner()
`},
		{"inactive nested falls back to root", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library
    static if false:
        module Library:
            public:
                def inner() -> i64: return 2
    def run() -> i64: return value()
`},
		{"conditional nested selective", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library::inner
    static if true:
        module Library:
            public:
                def inner() -> i64: return 2
    def run() -> i64: return inner()
`},
		{"conditional nested shadows root", `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library
    static if true:
        module Library:
            public:
                def inner() -> i64: return 2
    def run() -> i64: return inner()
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "using.elisa", tc.source, AnalyzeOptions{})
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestUsingConflictAfterConditionalResolution(t *testing.T) {
	src := `module Library:
    public:
        def value() -> i64: return 1
using Library::value
module Client:
    using Library::value
    static if true:
        module Library:
            public:
                def value() -> i64: return 2
    def run() -> i64: return value()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "using_conflict.elisa", src, AnalyzeOptions{})
	found := false
	for _, err := range result.Errors() {
		if strings.Contains(err, "conflicting using") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing conflict between distinct resolved targets: %v", result.Errors())
	}
}

func TestUsingImportConflictAfterConditionalResolution(t *testing.T) {
	base := `module Library:
    public:
        def value() -> i64: return 1
module Client:
    using Library::value
    static if true:
        module Library:
            public:
                def value() -> i64: return 2
    def run() -> i64: return value()
`
	for _, src := range []string{"from Library import value\n" + base, base + "from Library import value\n"} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "using_import.elisa", src, AnalyzeOptions{})
		found := false
		for _, err := range result.Errors() {
			if strings.Contains(err, "conflicting ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing conflict between using and import: %v", result.Errors())
		}
	}
}
