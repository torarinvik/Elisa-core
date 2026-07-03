package semantic_test

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeRejectsNamedConstructorArgsForUnnamedEnumPayloads(t *testing.T) {
	src := `enum MaybeInt:
	Some(int)

def make_some() -> MaybeInt:
	return MaybeInt.Some(value: 1)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_unnamed_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum constructor \"MaybeInt.Some\" does not declare named payload fields") {
		t.Fatalf("expected named-constructor diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsMixedNamedAndPositionalEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(left: 3, 4)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_mixed_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum constructor \"PairOrInt.Pair\" cannot mix positional and named arguments") {
		t.Fatalf("expected mixed-argument diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsNamedArgumentsForFunctionCalls(t *testing.T) {
	src := `extern add(left: int, right: int) -> int

def bad() -> int:
	return add(left: 1, right: 2)
`
	result, errs := parseAndAnalyze(t, "named_args_function_call.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "bad", "int")
}
func TestAnalyzeRejectsShadowedStatementMatchArms(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)

def unwrap(value: MaybeInt) -> int:
	match value:
		MaybeInt.Some(first):
			return first
		MaybeInt.Some(second):
			return second
		MaybeInt.None:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_match_shadowed_stmt.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "match arm \"MaybeInt.Some(second)\" is unreachable because an earlier arm already matches it") {
		t.Fatalf("expected shadowed-arm diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsConstEnumMatchStatements(t *testing.T) {
	src := `const enum Op of i32:
	ADD = 1
	SUB = 2
	MUL = 3

def score_stmt(op: Op) -> int:
	match op:
		Op.ADD:
			return 10
		Op.SUB:
			return 20
		_:
			return 30
	return 0

def score_full(op: Op) -> int:
	match op:
		Op.ADD:
			return 10
		Op.SUB:
			return 20
		Op.MUL:
			return 30
	return 0
`
	_, errs := parseAndAnalyze(t, "const_enum_match_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsStringLiteralMatchStatement(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"local":
			return 1
	return 0
`
	_, errs := parseAndAnalyze(t, "string_match_stmt_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsStringLiteralMatchStatementOverSlice(t *testing.T) {
	src := `def classify(text: cstr[row]) -> int:
	match text[0:2]:
		"if":
			return 1
		_:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "string_match_stmt_slice_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsNonExhaustiveStringMatchStatementWithFallthrough(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"local":
			return 1
	return 0
`
	_, errs := parseAndAnalyze(t, "string_match_stmt_non_exhaustive.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsShadowedStringMatchArms(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"local":
			return 1
		"local":
			return 2
		_:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "string_match_shadowed_stmt.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "match arm \"local\" is unreachable because an earlier arm already matches it") {
		t.Fatalf("expected shadowed string-arm diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsNestedStringLiteralPatterns(t *testing.T) {
	src := `enum Wrapper:
	Text(StringView)
	Other

def classify(value: Wrapper) -> int:
	match value:
		Wrapper.Text("local"):
			return 1
		Wrapper.Other:
			return 0
	return 0
`
	result, errs := parseAndAnalyze(t, "string_match_nested_pattern_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsStringMatchOverNonStringValue(t *testing.T) {
	src := `def classify(value: int) -> int:
	match value:
		"local":
			return 1
		_:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "string_match_non_string_value_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	// Matching an integer is now valid (open integer match), so a string-literal arm over an int
	// is rejected as a malformed integer-match arm rather than an unmatchable scrutinee.
	if !strings.Contains(all, "top-level integer match arm must use an integer literal or _") {
		t.Fatalf("expected integer-match arm diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsNonLiteralTopLevelStringMatchArm(t *testing.T) {
	src := `enum Token:
	Region

def classify(text: StringView) -> int:
	match text:
		Token.Region:
			return 1
		_:
			return 0
`
	_, errs := parseAndAnalyze(t, "string_match_non_literal_arm_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "top-level string match arm must use a string literal or _") {
		t.Fatalf("expected top-level string-arm diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeStructIsConditionBindsFieldsInIfAndWhile(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Span:
	start: i64
	finish: i64

struct Token:
	kind: Tok
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, span: Span(start: start), value: value):
		return start + value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value):
		return value
	return 0
`
	result, errs := parseAndAnalyze(t, "struct_is_condition_bindings_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeStructIsConditionBindingsFlowThroughAnd(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Token:
	kind: Tok
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) and value > 0:
		return value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value) and value > 0:
		return value
	return 0
`
	result, errs := parseAndAnalyze(t, "struct_is_condition_bindings_and_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeStructIsConditionBindingsFlowThroughTruthyOr(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Token:
	kind: Tok
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT, value: value):
		return value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT, value: value):
		return value
	return 0
`
	result, errs := parseAndAnalyze(t, "struct_is_condition_bindings_or_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeStructIsConditionBindingDiagnosticForTruthyOrMissingBranchBinding(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Token:
	kind: Tok
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT):
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "struct_is_condition_binding_or_missing_branch_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected composed-condition binding diagnostic")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "identifier \"value\" is not available here because truthy `or` branches do not agree on that binding: left branch binds it as i64, while right branch does not bind it") {
		t.Fatalf("expected truthy-or missing-branch binding diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeStructIsConditionBindingDiagnosticForTruthyOrTypeMismatch(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Span:
	start: i64
	finish: i64

struct Token:
	kind: Tok
	value: i64
	span: Span

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT, span: value):
		return value.start
	return 0
`
	_, errs := parseAndAnalyze(t, "struct_is_condition_binding_or_type_mismatch_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected composed-condition binding diagnostic")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "identifier \"value\" is not available here because truthy `or` branches do not agree on that binding: left branch binds it as i64, while right branch binds it as Span") {
		t.Fatalf("expected truthy-or type-mismatch binding diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "undefined identifier \"value\"") {
		t.Fatalf("expected specialized diagnostic instead of generic undefined identifier, got:\n%s", all)
	}
}

// docs/76 Phase 3 / docs/77: a plain `enum` whose variant contains the enum by value is now
// automatically promoted to the region-backed (packed) machinery (RecursivePlain flag). The old
// "cannot contain Expr by value" rejection no longer fires — the declaration is accepted.
func TestAnalyzeAcceptsRecursiveEnumPayloadByValueAsRegionBacked(t *testing.T) {
	src := `enum Expr:
	Int(int)
	Add(Expr, Expr)
`
	_, errs := parseAndAnalyze(t, "enum_recursive_by_value_promoted.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsRecursiveEnumPayloadByReference(t *testing.T) {
	src := `enum Expr:
	Int(value: int)
	Add(left: Expr&, right: Expr&)

def eval(node: Expr&) -> int:
	match node[0]:
		Expr.Int(value: value):
			return value
		Expr.Add(left: left, right: right):
			return eval(left) + eval(right)
`
	_, errs := parseAndAnalyze(t, "enum_recursive_by_ref_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumsWithExplicitStoreCore(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	one: Expr = new[store] Expr.Int(value: 1)
	two: Expr = new[store] Expr.Int(value: 2)
	return new[store] Expr.Add(left: one, right: two)

def eval(node: Expr, store: Expr.Store[Local]) -> int:
	match node in store:
		Expr.Int(value: value):
			return value
		Expr.Add(left: left, right: right):
			return eval(left, store) + eval(right, store)
`
	_, errs := parseAndAnalyze(t, "packed_enum_explicit_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumsWithinInStoreBlocks(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 1)
		right: Expr = new Expr.Int(span: 2, value: 2)
		return new Expr.Add(span: 3, left: left, right: right)

def eval(node: Expr, store: Expr.Store[Local]) -> int:
	in store:
		match node:
			Expr.Int(value: value):
				return value + node.span
			Expr.Add(left: left, right: right):
				return node.span + eval(left, store) + eval(right, store)
`
	_, errs := parseAndAnalyze(t, "packed_enum_in_store_block_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumTailPayloadsAsDynamicViews(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def build() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Block(count: 3, items: [1, 2, 3])
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.Block(count: count, items: items):
			if items.len == count:
				return items[0] + items[2]
			return 0
`
	result, errs := parseAndAnalyze(t, "packed_enum_tail_payload_dview_ok.elisa", src)
	requireNoErrors(t, errs)

	enumType, ok := result.NamedTypes["Expr"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Expr enum type, got %T", result.NamedTypes["Expr"])
	}
	variant, ok := enumType.Variant("Block")
	if !ok || variant == nil {
		t.Fatal("expected Expr.Block variant metadata")
	}
	if !variant.HasTailPayload() {
		t.Fatal("expected Expr.Block to record a tail payload")
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok || tailIndex != 1 {
		t.Fatalf("expected Expr.Block tail payload index 1, got %d (ok=%v)", tailIndex, ok)
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		t.Fatal("expected Expr.Block tail payload to lower as view")
	}
	if viewType.String() != "view[int]" {
		t.Fatalf("expected Expr.Block tail payload type view[int], got %s", viewType.String())
	}
}
func TestAnalyzeRejectsTailPayloadsOnOrdinaryEnums(t *testing.T) {
	src := `enum Expr:
	Block(items: tail int)
`
	_, errs := parseAndAnalyze(t, "enum_tail_payload_non_packed_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum \"Expr\" variant \"Block\" tail payloads are only supported for packed enums") {
		t.Fatalf("expected ordinary-enum tail payload diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsNonFinalPackedEnumTailPayloads(t *testing.T) {
	src := `packed enum Expr:
	Block(items: tail int, count: usize)

def build() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Block(items: [1, 2, 3], count: 3)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.Block(items: items, count: count):
			if items.len == count:
				return items[1]
			return 0
`
	result, errs := parseAndAnalyze(t, "packed_enum_tail_payload_not_final_ok.elisa", src)
	requireNoErrors(t, errs)

	enumType, ok := result.NamedTypes["Expr"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Expr enum type, got %T", result.NamedTypes["Expr"])
	}
	variant, ok := enumType.Variant("Block")
	if !ok || variant == nil {
		t.Fatal("expected Expr.Block variant metadata")
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok || tailIndex != 0 {
		t.Fatalf("expected Expr.Block tail payload index 0, got %d (ok=%v)", tailIndex, ok)
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		t.Fatal("expected Expr.Block tail payload to lower as view")
	}
	if viewType.String() != "view[int]" {
		t.Fatalf("expected Expr.Block tail payload type view[int], got %s", viewType.String())
	}
}
func TestAnalyzeAcceptsFreezeOfLocalPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def freeze_store(owner: Arena) -> Expr.Store[Frozen]:
	store: Expr.Store[Local] = Expr.Store(owner)
	return freeze(move store)
`
	result, errs := parseAndAnalyze(t, "packed_enum_store_freeze_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "freeze_store", "Expr.Store[Frozen]")
}
