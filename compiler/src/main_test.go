package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"llcontext/src/backend"
)

func repoRootFromMainTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func TestRunCLICompilesFixtureProgramsToLLVM(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixtures := []struct {
		name   string
		path   string
		checks []string
	}{
		{
			name: "pointer_alloc",
			path: filepath.Join(repoRoot, "Code", "test_programs", "pointer_alloc.llcontext"),
			checks: []string{
				"%ErrUnion__MemoryError__heap_Node = type { i32, ptr }",
				"%ErrUnion__MemoryError__int = type { i32, i64 }",
				"%Node = type { i64, ptr }",
				"declare ptr @alloc_node()",
				"declare ptr @sfree_node(ptr)",
				"define i32 @require_node(ptr ",
				"define i32 @make_node_value(ptr ",
				"define i64 @make_node_value_or_zero()",
				"define void @release_node(ptr",
			},
		},
		{
			name: "shape_ops",
			path: filepath.Join(repoRoot, "Code", "test_programs", "shape_ops.llcontext"),
			checks: []string{
				"%ErrUnion__ShapeOpError__",
				"%DynArray__i32 = type { ptr, i64, i64 }",
				"declare i32 @resize(",
				"declare i32 @push(",
				"define i32 @grow_once(",
				"define i32 @merge_strings(",
			},
		},
		{
			name: "variadic_stdio",
			path: filepath.Join(repoRoot, "Code", "test_programs", "variadic_stdio.llcontext"),
			checks: []string{
				"%ErrUnion__FormatError__int = type { i32, i64 }",
				"declare i64 @snprintf(ptr, i64, ptr, ...)",
				"define i32 @checked_format_len(",
				"define i64 @format_len(ptr",
				"define i32 @checked_write_into(",
				"define i64 @write_into(ptr %0, i64 %1, ptr %2)",
				"call i64 (ptr, i64, ptr, ...) @snprintf(",
			},
		},
		{
			name: "allocator_ownership",
			path: filepath.Join(repoRoot, "Code", "test_programs", "allocator_ownership.llcontext"),
			checks: []string{
				"%HeapPairNode = type { %FuzzPair, ptr }",
				"declare ptr @alloc_heap_pair_node()",
				"declare ptr @sfree_heap_pair_node(ptr)",
				"declare ptr @alloc_bytes(i64)",
				"declare ptr @sfree_bytes(ptr)",
				"declare i64 @snprintf(ptr, i64, ptr, ...)",
				"define i32 @build_pair_chain_sum(ptr ",
				"define i32 @borrow_then_release_single_pair(ptr ",
				"@recursive_format_or_fallback(",
				"@allocator_ownership_combo(",
			},
		},
		{
			name: "pointer_casts",
			path: filepath.Join(repoRoot, "Code", "test_programs", "pointer_casts.llcontext"),
			checks: []string{
				"define i64 @ptr_bits(ptr",
				"ptrtoint ptr",
				"define ptr @bits_ptr(i64",
				"inttoptr i64",
				"define ptr @advance_raw(ptr %0, i64 %1)",
				"getelementptr i8, ptr",
			},
		},
		{
			name: "stack_pointers",
			path: filepath.Join(repoRoot, "Code", "test_programs", "stack_pointers.llcontext"),
			checks: []string{
				"%ErrUnion__StackError__int = type { i32, i64 }",
				"%ScratchSlot = type { i64 }",
				"define i32 @checked_stack_slot(ptr",
				"define i64 @stack_slot_or_zero()",
				"alloca %ScratchSlot",
			},
		},
		{
			name: "nested_access",
			path: filepath.Join(repoRoot, "Code", "test_programs", "nested_access.llcontext"),
			checks: []string{
				"declare %DynArray__i32 @make_array()",
				"declare %DynArrayView @make_array_view()",
				"call %DynArray__i32 @make_array()",
				"call %DynArrayView @make_array_view()",
				"call %DynArrayView @arena_da_view(ptr",
				"alloca %DynArray__i32",
				"alloca %DynArrayView",
			},
		},
		{
			name: "typed_list_views",
			path: filepath.Join(repoRoot, "Code", "test_programs", "typed_list_views.llcontext"),
			checks: []string{
				"define i32 @head_of_middle(%DynArray__i32",
				"declare %DynArrayView @arena_da_view(ptr, i64, i64)",
				"call %DynArrayView @arena_da_view(ptr",
				"define i64 @inferred_literal_head()",
				"alloca [4 x i64]",
				"getelementptr [4 x i64], ptr",
				"insertvalue %DynArrayView",
			},
		},
		{
			name: "string_view_ops",
			path: filepath.Join(repoRoot, "Code", "test_programs", "string_view_ops.llcontext"),
			checks: []string{
				"%StringView = type { ptr, i64 }",
				"declare %StringView @ctx_string_view(ptr, i64, i64)",
				"call %StringView @ctx_string_view(ptr",
				"declare i64 @ctx_string_view_index(%StringView, i64)",
				"declare i64 @ctx_strlen(ptr)",
				"declare i64 @ctx_string_view_eq(%StringView, ptr)",
				"declare i64 @ctx_string_views_eq(%StringView, %StringView)",
			},
		},
		{
			name: "export_vec2i",
			path: filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext"),
			checks: []string{
				"define %Vec__i32 @vec_add_i32(%Vec__i32",
				"define %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
				"define i64 @vec2i_add(i64",
				"define i64 @vec2i_keep_left(i64",
			},
		},
		{
			name: "dict_runtime",
			path: filepath.Join(repoRoot, "Code", "test_programs", "dict_runtime.llcontext"),
			checks: []string{
				"%DynDict__i32 = type { ptr, i64, i64, i64, ptr }",
				"%ErrUnion__RuntimeError__any_i32 = type { i32, ptr }",
				"define %DynDict__i32 @arena_dict_new__i32(",
				"define i32 @arena_dict_reserve__i32(",
				"define ptr @arena_dict_get__i32(",
				"define i32 @arena_dict_put__i32(",
				"define i1 @arena_dict_contains__i32(",
				"define i1 @arena_dict_remove__i32(",
				"define void @arena_dict_clear__i32(",
				"define i32 @touch_dict(ptr ",
				"call %DynDict__i32 @arena_dict_new__i32(ptr",
				"call i32 @arena_dict_put__i32(ptr",
			},
		},
		{
			name: "frontend_lexer",
			path: filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext"),
			checks: []string{
				"%FrontendPos = type { i64, i64, i64 }",
				"%FrontendToken = type { i32, %FrontendPos, %StringView, %StringView }",
				"define i64 @frontend_advance_char(ptr",
				"define %FrontendToken @frontend_next_token(ptr",
				"define %DynArray__FrontendToken @frontend_tokenize(ptr",
				"define i64 @frontend_lexer_parity_suite()",
				"define i64 @frontend_lexer_token_count(ptr ",
				"define i64 @frontend_lexer_token_checksum(ptr ",
				"define i64 @frontend_lexer_token_kind_at(ptr ",
				"define i64 @frontend_lexer_copy_token_kinds(ptr ",
			},
		},
		{
			name: "compiler_parallel_fixture",
			path: filepath.Join(repoRoot, "Code", "test_programs", "compiler_parallel_fixture.llcontext"),
			checks: []string{
				"%Expr = type { i32, i64, [2 x i64] }",
				"%FrozenExprGraph = type { %Expr__Store, ptr }",
				"%Thread__i64__Joinable = type { i64, ptr }",
				"%Task__i64__Pending = type { i64, ptr }",
				"define %FrozenExprGraph @build_frozen_expr_graph(",
				"define i64 @expr_sum(%FrozenExprGraph",
				"define i64 @expr_depth(%FrozenExprGraph",
				"define i64 @compiler_parallel_fixture(ptr",
				"define %Thread__i64__Joinable @spawn1__FrozenExprGraph__i64(",
				"define %Task__i64__Pending @pool_submit1__FrozenExprGraph__i64(",
				"define void @task_group_add__i64(",
				"call i64 @join__i64(",
				"call i64 @pool_await__i64(",
			},
		},
		{
			name: "frontend_stress",
			path: filepath.Join(repoRoot, "Code", "test_programs", "frontend_stress.llcontext"),
			checks: []string{
				"%SourceSpan = type { i64, i64 }",
				"%Token = type { i32, %SourceSpan, ptr }",
				"%DynDict__Symbol = type { ptr, i64, i64, i64, ptr }",
				"%Scope = type { ptr, %DynDict__Symbol, i64 }",
				"%ParserState = type { %DynArrayView, i64, ptr }",
				"define %DynArrayView @make_tokens()",
				"define i32 @frontend_scope_stress(ptr",
				"define i64 @frontend_region_token(i64",
				"define i32 @frontend_smoke(ptr",
				"define %DynDict__Symbol @arena_dict_new__Symbol(ptr",
				"define i32 @arena_dict_put__Symbol(ptr",
				"define i1 @arena_dict_contains__Symbol(ptr",
				"call ptr @new_region(i64 2048)",
				"call ptr @arena_alloc(ptr",
			},
		},
		{
			name: "recursive_enum",
			path: filepath.Join(repoRoot, "Code", "test_programs", "recursive_enum.llcontext"),
			checks: []string{
				"%Expr = type { i32, [2 x i64] }",
				"define i64 @eval(ptr",
				"define i64 @make_sum()",
				"call ptr @arena_alloc(ptr",
				"call i64 @eval(ptr",
			},
		},
		{
			name: "region_checkpoints",
			path: filepath.Join(repoRoot, "Code", "test_programs", "region_checkpoints.llcontext"),
			checks: []string{
				"%Arena = type { ptr, ptr, i64 }",
				"%ArenaMark = type { ptr, i64 }",
				"declare %ArenaMark @arena_snapshot(ptr)",
				"declare void @arena_rewind(ptr, %ArenaMark)",
				"declare void @arena_reset(ptr)",
				"call %ArenaMark @arena_snapshot(ptr",
				"call void @arena_rewind(ptr",
				"call void @arena_reset(ptr",
			},
		},
		{
			name: "region_ref_types",
			path: filepath.Join(repoRoot, "Code", "test_programs", "region_ref_types.llcontext"),
			checks: []string{
				"%RegionNode = type { ptr, i32 }",
				"define i32 @region_ref_sum(i32 ",
				"call ptr @arena_alloc(ptr",
				"load i32, ptr",
			},
		},
		{
			name: "packed_enum_common",
			path: filepath.Join(repoRoot, "Code", "test_programs", "packed_enum_common.llcontext"),
			checks: []string{
				"%Expr = type { i32, i64, [2 x i64] }",
				"%Token = type { i32, i64 }",
				"define ptr @build_expr(%Arena",
				"define i64 @eval(ptr",
				"define i64 @packed_demo()",
				"call ptr @arena_alloc(ptr",
				"load i64, ptr",
			},
		},
		{
			name: "json_parser",
			path: filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext"),
			checks: []string{
				"%JsonCursor = type { ptr, i64, i64 }",
				"%JsonLexemeResult = type { i64, i64, i64 }",
				"%JsonNode = type { i32, i64, i64, i64, [3 x i64] }",
				"%JsonParseNodeResult = type { ptr, i64 }",
				"define %JsonLexemeResult @json_parse_string_lexeme(ptr",
				"define %JsonLexemeResult @json_parse_number_lexeme(ptr",
				"define i64 @json_parse_string(ptr",
				"define i64 @json_parse_number(ptr",
				"define i64 @json_parse_array(ptr",
				"define i64 @json_parse_object(ptr",
				"define %JsonParseNodeResult @json_parse_value_node(ptr",
				"define %JsonParseNodeResult @json_parse_array_node(ptr",
				"define %JsonParseNodeResult @json_parse_object_node(ptr",
				"define i8 @json_ast_kind(ptr %0, %JsonNode__Store %1)",
				"define ptr @json_ast_array_nth(ptr %0, i64 %1, %JsonNode__Store %2)",
				"define ptr @json_ast_object_get(ptr %0, ptr %1, ptr %2, %JsonNode__Store %3)",
				"define i64 @json_parser_parity_suite()",
				"define i64 @json_parser_checksum(ptr",
				"define i64 @json_parser_ast_checksum(ptr",
				"define i64 @json_parallel_worker(%JsonParallelJob",
				"define i64 @json_parser_parallel_max_workers()",
				"define i64 @json_parser_parallel_checksum(",
				"define i64 @json_parser_parallel_ast_checksum(",
				"call %ThreadPool @pool_new(",
				"call void @task_group_wait_all_raw(ptr",
			},
		},
		{
			name: "string_escapes",
			path: filepath.Join(repoRoot, "Code", "test_programs", "string_escapes.llcontext"),
			checks: []string{
				"define ptr @newline_text()",
				"define ptr @quoted_text()",
				"define ptr @unicode_text()",
			},
		},
		{
			name: "value_optionals",
			path: filepath.Join(repoRoot, "Code", "test_programs", "value_optionals.llcontext"),
			checks: []string{
				"%Box = type { i64 }",
				"%Optional__int = type { i1, i64 }",
				"%Optional__Box = type { i1, %Box }",
				"define %Optional__int @maybe_value(i1",
				"define i64 @fallback_value(i1",
				"extractvalue %Optional__int",
				"define %Optional__Box @maybe_box(i1",
				"define i64 @unwrap_or(i1",
				"getelementptr inbounds nuw %Optional__Box",
				"getelementptr inbounds nuw %Box",
			},
		},
		{
			name: "concurrency_explicit",
			path: filepath.Join(repoRoot, "Code", "test_programs", "concurrency_explicit.llcontext"),
			checks: []string{
				"%Thread__i64__Joinable = type { i64, ptr }",
				"%Task__i64__Pending = type { i64, ptr }",
				"%ThreadPool = type { ptr }",
				"%TaskGroup = type { ptr, ptr }",
				"%Mutex = type { ptr }",
				"%MutexGuard__Held = type { ptr }",
				"%CondVar = type { ptr }",
				"%atomic__i64 = type { i64 }",
				"%ConcurrencyWorkStart1 = type { i64, ptr, ptr, %atomic__i64 }",
				"%ConcurrencyTaskGroupNode = type { ptr, ptr }",
				"%ConcurrencyBox__i64 = type { i64 }",
				"define i64 @atomic_roundtrip()",
				"define i1 @concurrency_handles(",
				"define %Thread__i64__Joinable @typed_thread_spawn(i64",
				"define %Thread__i64__Joinable @typed_thread_spawn_permissioned(i64",
				"define i64 @typed_thread_join(%Thread__i64__Joinable",
				"define void @typed_thread_detach_permissioned(i64",
				"define i64 @typed_pool_roundtrip(ptr",
				"define void @typed_group_waitall(ptr",
				"define i64 @typed_pool_scope(i64",
				"define i64 @runtime_carrier_worker(%SharedGate",
				"define i64 @typed_runtime_carrier_transfer(ptr",
				"define void @typed_notify_scope(%CondVar",
				"define i64 @permissioned_thread_worker(i64",
				"define %Thread__i64__Joinable @spawn1__i64__i64(",
				"define i64 @join__i64(",
				"define void @detach__i64(",
				"define %Task__i64__Pending @pool_submit1__i64__i64(",
				"define i64 @pool_await__i64(",
				"declare void @notify_one(ptr)",
				"declare void @notify_all(ptr)",
				"call void @pool_shutdown(ptr",
				"define %TaskGroup @task_group_new()",
				"define void @task_group_add__i64(",
				"define void @task_group_wait_all(ptr",
				"define ptr @ctx_concurrency_work1_entry__i64__i64(",
				"define i64 @ctx_concurrency_work1_take_result__i64(",
				"call void @store__i64(",
				"call i1 @compare_exchange__i64(",
				"call i64 @exchange__i64(",
				"call i64 @fetch_add(",
				"call i64 @fetch_sub(",
				"call i64 @fetch_or(",
				"call i64 @fetch_and(",
				"call i64 @fetch_xor(",
				"call i64 @load__i64(",
				"call void @fence(i32",
			},
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "llvm", fixture.path}, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			output := stdout.String()
			for _, check := range fixture.checks {
				if !strings.Contains(output, check) {
					t.Fatalf("expected output to contain %q, got:\n%s", check, output)
				}
			}
		})
	}
}

func TestRunCLIEmitsBitcodeAndObjectForFixtureProgram(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "pointer_alloc.llcontext")
	outputDir := t.TempDir()
	bitcodePath := filepath.Join(outputDir, "pointer_alloc.bc")
	objectPath := filepath.Join(outputDir, "pointer_alloc.o")

	tests := []struct {
		name       string
		args       []string
		outputPath string
		check      func(*testing.T, []byte)
	}{
		{
			name:       "bitcode",
			args:       []string{"-emit", "bc", "-o", bitcodePath, fixturePath},
			outputPath: bitcodePath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if !looksLikeBitcodeFile(data) {
					t.Fatalf("expected bitcode magic prefix, got % x", data[:min(len(data), 4)])
				}
			},
		},
		{
			name:       "object",
			args:       []string{"-emit", "obj", "-o", objectPath, fixturePath},
			outputPath: objectPath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if !looksLikeObjectFile(data) {
					t.Fatalf("expected native object file magic, got % x", data[:min(len(data), 4)])
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(test.args, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected binary emit mode not to write stdout, got:\n%s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			data, err := os.ReadFile(test.outputPath)
			if err != nil {
				t.Fatalf("expected output file %s to exist: %v", test.outputPath, err)
			}
			if len(data) < 4 {
				t.Fatalf("expected non-empty output file, got %d bytes", len(data))
			}
			test.check(t, data)
		})
	}
}

func TestRunCLIEmitsHeaderForExportFixture(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext")
	outputPath := filepath.Join(t.TempDir(), "export_vec2i.h")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "header", "-o", outputPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected header emit with -o not to write stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected header output file %s to exist: %v", outputPath, err)
	}
	header := string(data)
	checks := []string{
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
}

func TestParseArgsAcceptsOptimizationShorthands(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		level int
	}{
		{name: "shorthand", args: []string{"-O3", "fixture.llcontext"}, level: 3},
		{name: "equals", args: []string{"-O=2", "fixture.llcontext"}, level: 2},
		{name: "separate", args: []string{"-O", "0", "fixture.llcontext"}, level: 0},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options, err := parseArgs(test.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if !options.hasOptLevel {
				t.Fatal("expected optimization flag to be marked as explicitly set")
			}
			if int(options.optLevel) != test.level {
				t.Fatalf("expected opt level O%d, got O%d", test.level, int(options.optLevel))
			}
		})
	}
}

func TestParseArgsAcceptsPackedABI(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want backend.PackedEnumABI
	}{
		{name: "equals", args: []string{"-packed-abi=word-handle", "fixture.llcontext"}, want: backend.PackedEnumABIWordHandle},
		{name: "separate", args: []string{"-packed-abi", "row-handle", "fixture.llcontext"}, want: backend.PackedEnumABIRowHandle},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options, err := parseArgs(test.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if options.packedABI != test.want {
				t.Fatalf("expected packed ABI %q, got %q", test.want, options.packedABI)
			}
		})
	}
}

func TestRunCLICompilesJSONParserWithPackedWordHandleABI(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", "-packed-abi", "word-handle", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	checks := []string{
		"%JsonNode__Store = type { ptr, i64, ptr }",
		"%PackedStoreAllocResult = type { ptr, i64 }",
		"%JsonParseNodeResult = type { i64, i64 }",
		"define %JsonParseNodeResult @json_parse_value_node(ptr",
		"define %JsonParseNodeResult @json_parse_array_node(ptr",
		"define %JsonParseNodeResult @json_parse_object_node(ptr",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state)",
		"call i64 @ctx_packed_store_read_word(",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"define ptr @json_parse_value_node(ptr", "define ptr @json_parse_array_node(ptr", "define ptr @json_parse_object_node(ptr"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed word-handle CLI path to avoid %q, got:\n%s", bad, output)
		}
	}
	for _, bad := range []string{"call %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr %packed.alloc.store.arena, i64 %packed.alloc.store.row_bytes, ptr %packed.alloc.store.state)"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed word-handle CLI path to use the fixed-row constructor helper and avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestEffectiveOptimizationLevelDefaultsByEmitMode(t *testing.T) {
	tests := []struct {
		name     string
		emit     string
		explicit bool
		level    int
		expect   int
	}{
		{name: "llvm default raw", emit: emitLLVM, expect: 0},
		{name: "bitcode default optimized", emit: emitBitcode, expect: 3},
		{name: "object default optimized", emit: emitObject, expect: 3},
		{name: "explicit overrides default", emit: emitObject, explicit: true, level: 2, expect: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options := cliOptions{emit: test.emit}
			if test.explicit {
				options.hasOptLevel = true
				options.optLevel = backend.OptimizationLevel(test.level)
			}
			if got := int(effectiveOptimizationLevel(options)); got != test.expect {
				t.Fatalf("expected effective opt level O%d, got O%d", test.expect, got)
			}
		})
	}
}

func TestRunCLIGeneratedHeaderInteropHarness(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i_generated_harness.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "export_vec2i.h")
	objectPath := filepath.Join(outputDir, "export_vec2i.o")
	exePath := filepath.Join(outputDir, "export_vec2i_generated_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		{"-emit", "obj", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileCmd := exec.Command(clangPath, "-I", outputDir, harnessPath, objectPath, "-o", exePath)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated-header interop harness failed: %v\n%s", err, string(runOutput))
	}
}

func TestRunCLIFrontendLexerGeneratedHeaderInteropHarness(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer_generated_harness.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "frontend_lexer_runtime_shims.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "frontend_lexer.h")
	objectPath := filepath.Join(outputDir, "frontend_lexer.o")
	exePath := filepath.Join(outputDir, "frontend_lexer_generated_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		{"-emit", "obj", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-I", outputDir, harnessPath, shimPath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("frontend-lexer generated-header interop harness failed: %v\n%s", err, string(runOutput))
	}
}

func TestRunCLIJSONParserGeneratedHeaderInteropHarness(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser_generated_harness.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	runtimePath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_generated_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		{"-emit", "obj", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-pthread", "-I", outputDir, harnessPath, shimPath, runtimePath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("json-parser generated-header interop harness failed: %v\n%s", err, string(runOutput))
	}
}

func TestRunCLIJSONParserParallelBenchSmoke(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")
	benchPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_parallel_bench.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	runtimePath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_parallel_bench")
	jsonPath := filepath.Join(outputDir, "sample.json")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		{"-emit", "obj", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-pthread", "-I", outputDir, benchPath, shimPath, runtimePath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	if err := os.WriteFile(jsonPath, []byte("{\"items\":[1,2,3],\"ok\":true}\n"), 0o644); err != nil {
		t.Fatalf("failed to write sample json: %v", err)
	}

	for _, mode := range []string{"checksum", "ast-cached"} {
		runCmd := exec.Command(exePath, jsonPath, "4", "2", mode)
		runOutput, err := runCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("parallel json benchmark failed for mode %s: %v\n%s", mode, err, string(runOutput))
		}
		output := string(runOutput)
		for _, check := range []string{"mode=" + mode, "workers=2", "iterations=4", "MiB/s="} {
			if !strings.Contains(output, check) {
				t.Fatalf("expected parallel benchmark output to contain %q, got:\n%s", check, output)
			}
		}
	}
}

func TestRunCLIExecutesJSONParserSelfHostedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser_tests.llcontext")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to execute json parser tests successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] checksum_suite_matches_expected_values",
		"[ RUN      ] ast_checksum_matches_expected_values",
		"[ RUN      ] ast_and_checksum_paths_agree_on_nested_inputs",
		"[ RUN      ] invalid_inputs_are_rejected",
		"[ RUN      ] ast_raw_dom_helpers_expose_source_spans_and_structure",
		"[ RUN      ] ast_string_helpers_decode_escapes_and_match_unescaped_keys",
		"[ RUN      ] ast_number_helpers_materialize_integral_values_and_classify_edges",
		"[ SUMMARY  ] 7 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected json parser self-hosted test output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesStage1RuntimeToLLVM(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "contextlang_runtime.llcontext")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	checks := []string{
		"define ptr @int_to_string(i64",
		"define ptr @rt_concat2(ptr",
		"define ptr @rt_string_builder_new(ptr",
		"%StringView = type { ptr, i64 }",
		"define i64 @ctx_string_view_len(%StringView",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr",
		"define void @ctx_packed_store_alloc_result_slow(ptr",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(ptr",
		"define void @ctx_packed_store_alloc_fixed_result_slow(ptr",
		"%DynDict__i64 = type { ptr, i64, i64, i64, ptr }",
		"define %DynDict__i64 @arena_dict_new__i64(",
		"define i32 @arena_dict_reserve__i64(",
		"define ptr @arena_dict_get__i64(",
		"define i32 @arena_dict_put__i64(",
		"define i1 @arena_dict_contains__i64(",
		"define i1 @arena_dict_remove__i64(",
		"define void @arena_dict_clear__i64(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, check := range []string{
		"@rt_string_view_len = alias ",
		"@rt_string_from_view = alias ",
		"define i64 @rt_string_view_len(",
		"define ptr @rt_string_from_view(",
	} {
		if strings.Contains(output, check) {
			t.Fatalf("expected output to omit legacy string helper symbol %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIRejectsInvalidStringEscape(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_escape.llcontext")
	if err := os.WriteFile(fixturePath, []byte("def bad() -> any u8&:\n    return \"oops\\q\".cast[any u8&]()\n"), 0o644); err != nil {
		t.Fatalf("failed to write invalid fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid escape sequence \\\\q in string literal") {
		t.Fatalf("expected invalid escape diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIPrintsAnnotatedFunctionsInAST(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated.llcontext")
	src := "@test\ndef sample_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"@test", "def sample_case(0 params) -> void (1 stmts)"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIPrintsAnnotatedExternFunctionsInAST(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated_extern.llcontext")
	src := "repr(c) struct Holder:\n    value: any i32&\n\nrepr(c) struct Window:\n    items: view[Holder]\n\n@borrows_return(window.items[*])\nextern borrow_value(window: Window) -> view[Holder]\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated extern fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"@borrows_return(window.items[*])", "extern borrow_value(1 params) -> view[Holder]"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIPrintsConstEnumInAST(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_ast.llcontext")
	src := "const enum JsonNodeKind of i8:\n    Invalid = -1\n    Null\n    Bool = 1\n    String\n\ndef current_kind() -> JsonNodeKind:\n    return JsonNodeKind.String\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum AST fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"const enum JsonNodeKind of i8: (4 members)", "def current_kind(0 params) -> JsonNodeKind (1 stmts)"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesConstEnumSourceToLLVM(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_llvm.llcontext")
	src := "const enum JsonNodeKind of i8:\n    Invalid = -1\n    Null\n    Bool = 1\n    String\n\nconst DEFAULT_KIND: JsonNodeKind = JsonNodeKind.String\n\ndef kind_raw(kind: JsonNodeKind) -> i8:\n    return kind.i8()\n\ndef is_string(kind: JsonNodeKind) -> bool:\n    return kind == JsonNodeKind.String\n\ndef default_kind() -> JsonNodeKind:\n    return DEFAULT_KIND\n\ndef make_kind() -> JsonNodeKind:\n    return 1i8.cast[JsonNodeKind]()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum LLVM fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i8 @kind_raw(i8", "define i1 @is_string(i8", "define i8 @default_kind()", "define i8 @make_kind()", "ret i8 1"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIRejectsImplicitIntReturnToConstEnum(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_reject.llcontext")
	src := "const enum Kind of i8:\n    A\n\ndef bad() -> Kind:\n    return 0\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "expects Kind, got int") {
		t.Fatalf("expected const enum type mismatch diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIListsAnnotatedFunctionsByKind(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated_lists.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@fixture\ndef shared_seed() -> int:\n    return 7\n\n@bench\ndef bench_hot_loop() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated list fixture: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		contains []string
		omits    []string
	}{
		{
			name:     "tests",
			args:     []string{"-emit", "tests", fixturePath},
			contains: []string{"alpha_case\tfunc() -> void"},
			omits:    []string{"shared_seed", "bench_hot_loop"},
		},
		{
			name:     "benches",
			args:     []string{"-emit", "benches", fixturePath},
			contains: []string{"bench_hot_loop\tfunc() -> void"},
			omits:    []string{"alpha_case", "shared_seed"},
		},
		{
			name:     "fixtures",
			args:     []string{"-emit", "fixtures", fixturePath},
			contains: []string{"shared_seed\tfunc() -> int"},
			omits:    []string{"alpha_case", "bench_hot_loop"},
		},
		{
			name:     "tests filtered",
			args:     []string{"-emit", "tests", "-filter", "alpha", fixturePath},
			contains: []string{"alpha_case\tfunc() -> void"},
			omits:    []string{"bench_hot_loop", "shared_seed"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(test.args, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			output := stdout.String()
			for _, want := range test.contains {
				if !strings.Contains(output, want) {
					t.Fatalf("expected output to contain %q, got:\n%s", want, output)
				}
			}
			for _, omit := range test.omits {
				if strings.Contains(output, omit) {
					t.Fatalf("expected output not to contain %q, got:\n%s", omit, output)
				}
			}
		})
	}
}

func TestRunCLIRejectsFilterOutsideAnnotationListModes(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "filter_reject.llcontext")
	if err := os.WriteFile(fixturePath, []byte("def sample_case() -> void:\n    pass\n"), 0o644); err != nil {
		t.Fatalf("failed to write filter rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", "-filter", "sample", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-filter is only supported for -emit tests, benches, fixtures, test-runner, or test") {
		t.Fatalf("expected filter-mode diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIGeneratesTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "test_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n\ndef helper() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"@test",
		"def ctx_test_main() -> int can[Console.Write]:",
		"alpha_case()",
		"beta_case()",
		"export func main() -> int = ctx_test_main",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected test runner output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "\thelper()\n") {
		t.Fatalf("expected helper function not to be invoked by the generated runner, got:\n%s", output)
	}
}

func TestRunCLIGeneratesFilteredTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "filtered_test_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write filtered runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "beta_case()") {
		t.Fatalf("expected filtered runner to invoke beta_case, got:\n%s", output)
	}
	if strings.Contains(output, "\talpha_case()\n") {
		t.Fatalf("expected filtered runner not to invoke alpha_case, got:\n%s", output)
	}
	if !strings.Contains(output, "[ SUMMARY  ] 1 test(s) selected") {
		t.Fatalf("expected filtered runner summary, got:\n%s", output)
	}
}

func TestRunCLIRunsGeneratedTestRunner(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "generated_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write generated runner fixture: %v", err)
	}

	var runnerStdout bytes.Buffer
	var runnerStderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &runnerStdout, &runnerStderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to generate runner, stderr:\n%s", runnerStderr.String())
	}
	if runnerStderr.Len() != 0 {
		t.Fatalf("expected no stderr while generating runner, got:\n%s", runnerStderr.String())
	}

	runnerPath := filepath.Join(fixtureDir, "generated_runner.llcontext")
	if err := os.WriteFile(runnerPath, runnerStdout.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write generated runner source: %v", err)
	}
	objectPath := filepath.Join(fixtureDir, "generated_runner.o")

	var objectStdout bytes.Buffer
	var objectStderr bytes.Buffer
	exitCode = runCLI([]string{"-emit", "obj", "-o", objectPath, runnerPath}, &objectStdout, &objectStderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to compile generated runner, stderr:\n%s", objectStderr.String())
	}
	if objectStdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling generated runner, got:\n%s", objectStdout.String())
	}
	if objectStderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling generated runner, got:\n%s", objectStderr.String())
	}

	exePath := filepath.Join(fixtureDir, "generated_runner")
	compileCmd := exec.Command(clangPath, objectPath, "-o", exePath)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated test runner failed: %v\n%s", err, string(runOutput))
	}
	output := string(runOutput)
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"[       OK ] alpha_case",
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected generated runner output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to execute tests successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"[       OK ] alpha_case",
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected direct test execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesFilteredSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_filtered_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write filtered execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected filtered test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "[ RUN      ] alpha_case") {
		t.Fatalf("expected filtered execution not to run alpha_case, got:\n%s", output)
	}
	for _, check := range []string{
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 1 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected filtered execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIReturnsNonZeroWhenNoTestsMatchExecutionFilter(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_no_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write no-match execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected no-match test execution to fail, stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ NO TESTS ] no @test functions matched filter \"beta\"") {
		t.Fatalf("expected no-tests execution output, got:\n%s", stdout.String())
	}
}

func looksLikeObjectFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magics := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xfe, 0xed, 0xfa, 0xce},
	}
	for _, magic := range magics {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}

func looksLikeBitcodeFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.HasPrefix(data, []byte{'B', 'C'}) || bytes.Equal(data[:4], []byte{0xde, 0xc0, 0x17, 0x0b})
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
