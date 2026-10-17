//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"testing"
)

func TestLexicalBinderOwnershipAtActualPush(t *testing.T) {
	for _, domain := range []string{"type-param", "const-param", "shape-param", "assoc-type", "param-interface", "permission-param", "error-set-param", "region-param"} {
		t.Run(domain, func(t *testing.T) {
			fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
			index, err := IndexFunctionSource(fn, productContext())
			if err != nil {
				t.Fatal(err)
			}
			a := &Analyzer{lexicalBinders: &lexicalBinderOwnership{}}
			session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			request := lexicalBinderRequest{Index: index, Site: fn.Body[0], Role: "read", Domain: domain, Name: "T", Sources: []lexicalBinderSource{{index, fn, domain}}}
			publish := func() error {
				_, _, err := publishLexicalSnapshot(context.Background(), session, a, nil, []lexicalBinderRequest{request})
				return err
			}
			check := func() {
				if err := publish(); err != nil {
					t.Fatal(err)
				}
				// An unrelated source node is live and the same stack depth, yet cannot
				// relabel the frame that the helper actually created.
				request.Sources[0].Owner = fn.Body[0]
				if err := publish(); err == nil {
					t.Fatal("wrong live owner accepted")
				}
				request.Sources[0].Owner = fn
				request.Sources[0].Role = "other"
				if err := publish(); err == nil {
					t.Fatal("arbitrary frame role accepted")
				}
				request.Sources[0].Role = domain
				if err := publish(); err != nil {
					t.Fatal("recovery:", err)
				}
			}
			switch domain {
			case "type-param":
				a.withSourceTypeParams(fn, []string{"T"}, nil, check)
			case "const-param":
				a.withSourceConstParams(fn, []string{"T"}, nil, check)
			case "shape-param":
				a.withSourceShapeParams(fn, []string{"T"}, check)
			case "assoc-type":
				a.withSourceInterfaceAssocTypes(fn, map[string]Type{"T": nil}, check)
			case "param-interface":
				a.withSourceTypeParamInterfaces(fn, map[string]*StaticInterface{"T": nil}, check)
			case "permission-param":
				a.withSourcePermissionParams(fn, []string{"T"}, check)
			case "error-set-param":
				a.withSourceErrorSetParams(fn, []string{"T"}, check)
			case "region-param":
				a.withSourceRegionParams(fn, []string{"T"}, check)
			}
			if len(a.lexicalBinders.frames[domain]) != 0 {
				t.Fatal("ownership frame leaked after pop")
			}
			if err := publish(); err == nil {
				t.Fatal("popped frame remained authoritative")
			}
		})
	}
}

func TestLexicalBinderRejectsUnownedAndReplacedMaps(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	a := &Analyzer{lexicalBinders: &lexicalBinderOwnership{}}
	session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := lexicalBinderRequest{Index: index, Site: fn.Body[0], Role: "read", Domain: "type-param", Name: "T", Sources: []lexicalBinderSource{{index, fn, "type-param"}}}
	rejected := func() {
		t.Helper()
		if _, _, err := publishLexicalSnapshot(context.Background(), session, a, nil, []lexicalBinderRequest{request}); err == nil {
			t.Fatal("unowned frame admitted")
		}
	}
	a.withTypeParams([]string{"T"}, nil, rejected)
	a.withSourceTypeParams(fn, []string{"T"}, nil, func() {
		original := a.typeParamScopes[0]
		a.typeParamScopes[0] = map[string]Type{"T": original["T"]}
		rejected()
		a.typeParamScopes[0] = original
	})
	// Synthetic direct region pushes used by escape checks have no declaration
	// ownership; they must not borrow the surrounding function's identity.
	a.regionParamScopes = append(a.regionParamScopes, map[string]bool{"R": true})
	request.Domain = "region-param"
	request.Name = "R"
	request.Sources[0].Role = "region-param"
	rejected()
}

func TestLexicalGenericSourcePropagatesToChildFrames(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	a := &Analyzer{lexicalBinders: &lexicalBinderOwnership{}}
	params := []ast.GenericParam{{Name: "T", Kind: ast.GenericParamType}, {Name: "E", Kind: ast.GenericParamErrorSet}}
	a.withSourceGenericParams(fn, params, nil, func() {
		for _, domain := range []string{"type-param", "error-set-param"} {
			frames := a.lexicalBinders.frames[domain]
			if len(frames) != 1 || frames[0].owner != fn {
				t.Fatalf("missing %s ownership", domain)
			}
		}
	})
	for domain, frames := range a.lexicalBinders.frames {
		if len(frames) != 0 {
			t.Fatalf("leaked %s", domain)
		}
	}
}

func TestLexicalOwnershipInIncrementalAnalysis(t *testing.T) {
	const source = `def identity[T](value: T) -> T:
    return value

def work(value: i64) -> i64:
    return identity(value)
`
	session := NewIncrementalAnalysisSession()
	for _, text := range []string{source, "\n\n" + source} {
		reference := AnalyzeWithOptions(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
		got := session.Analyze(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
		if len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		assertIncrementalResultParity(t, reference, got)
	}
	if session.Stats().BinderFramesOwned == 0 {
		t.Fatal("normal incremental analysis did not record function binder ownership")
	}
}

func TestLexicalOwnershipNestedOwnerlessAndLateEnable(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	a := &Analyzer{lexicalBinders: &lexicalBinderOwnership{}}
	a.withSourceTypeParams(fn, []string{"T"}, nil, func() {
		outer := a.typeParamScopes[0]
		a.withTypeParams([]string{"T"}, nil, func() {
			a.withSourceTypeParams(fn.Body[0], []string{"T"}, nil, func() {
				sources := []lexicalBinderSource{{Owner: fn, Role: "type-param"}, {Owner: fn, Role: "type-param"}, {Owner: fn.Body[0], Role: "type-param"}}
				if err := a.lexicalBinders.validate("type-param", sources, []any{outer, a.typeParamScopes[1], a.typeParamScopes[2]}); err == nil {
					t.Fatal("ownerless middle frame admitted")
				}
			})
		})
		if err := a.lexicalBinders.validate("type-param", []lexicalBinderSource{{Owner: fn, Role: "type-param"}}, []any{outer}); err != nil {
			t.Fatal("outer ownership not restored:", err)
		}
	})
	// Enabling while an earlier untracked frame exists cannot align the new
	// recorder with the complete analyzer stack.
	a = &Analyzer{typeParamScopes: []map[string]Type{{"T": nil}}}
	a.lexicalBinders = &lexicalBinderOwnership{}
	a.withSourceTypeParams(fn, []string{"T"}, nil, func() {
		if err := a.lexicalBinders.validate("type-param", []lexicalBinderSource{{Owner: fn, Role: "type-param"}, {Owner: fn, Role: "type-param"}}, []any{a.typeParamScopes[0], a.typeParamScopes[1]}); err == nil {
			t.Fatal("late recorder accepted older frame")
		}
	})
}

func TestLexicalOwnershipEnabledInsideUntrackedHelper(t *testing.T) {
	a := &Analyzer{}
	a.withTypeParams([]string{"T"}, nil, func() {
		a.lexicalBinders = &lexicalBinderOwnership{}
		if err := a.lexicalBinders.validate("type-param", []lexicalBinderSource{{Role: "type-param"}}, []any{a.typeParamScopes[0]}); err == nil {
			t.Fatal("pre-existing frame gained authority")
		}
	})
	if len(a.typeParamScopes) != 0 {
		t.Fatal("untracked frame leaked")
	}
}
