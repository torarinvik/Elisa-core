package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"llcontext/src/backend"
)

func TestMain(m *testing.M) {
	prev, hadPrev := os.LookupEnv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Setenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS", "1")
	exitCode := m.Run()
	if hadPrev {
		_ = os.Setenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS", prev)
	} else {
		_ = os.Unsetenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
	}
	os.Exit(exitCode)
}

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
				"%DynDict__dstr_key_shape__i32 = type { ptr, i64, i64, i64, ptr }",
				"%ErrUnion__RuntimeError__i32 = type { i32, ptr }",
				"define %DynDict__dstr_key_shape__i32 @arena_dict_new__i32(",
				"define i32 @arena_dict_reserve__i32(",
				"define ptr @arena_dict_get__i32(",
				"define i32 @arena_dict_put__i32(",
				"define i1 @arena_dict_contains__i32(",
				"define i1 @arena_dict_remove__i32(",
				"define void @arena_dict_clear__i32(",
				"define i32 @touch_dict(ptr ",
				"call %DynDict__dstr_key_shape__i32 @arena_dict_new__i32(ptr",
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
				"%Expr = type { i32, i64, [1 x i64] }",
				"%FrozenExprGraph = type { %Expr__Store, i32 }",
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
				"%DynDict__dstr__Symbol = type { ptr, i64, i64, i64, ptr }",
				"%Scope = type { ptr, %DynDict__dstr__Symbol, i64 }",
				"%ParserState = type { %DynArrayView, i64, ptr }",
				"define %DynArrayView @make_tokens()",
				"define i32 @frontend_scope_stress(ptr",
				"define i64 @frontend_region_token(i64",
				"define i32 @frontend_smoke(ptr",
				"define %DynDict__dstr__Symbol @arena_dict_new__Symbol(ptr",
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
			name: "regular_enum_values",
			path: filepath.Join(repoRoot, "Code", "test_programs", "regular_enum_values.llcontext"),
			checks: []string{
				"%Small = type { i32, [2 x i64] }",
				"define %Small @make_node(i64",
				"define i64 @score(%Small",
				"define i64 @total(i64",
				"define i64 @main()",
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
				"%Expr = type { i32, i64, [1 x i64] }",
				"%Token = type { i32, i64 }",
				"define i32 @build_expr(%Arena",
				"define i64 @eval(i32",
				"define i64 @packed_demo()",
				"load i64, ptr",
			},
		},
		{
			name: "ref_qualifier_generics",
			path: filepath.Join(repoRoot, "Code", "test_programs", "ref_qualifier_generics.llcontext"),
			checks: []string{
				"%Handle__heap__anon = type { ptr }",
				"define %Handle__heap__anon @keep_handle__heap__anon(%Handle__heap__anon",
				"define i32 @peek_value(%Handle__heap__anon",
				"define i64 @keep_heap_handle(i64",
				"call %Handle__heap__anon @keep_handle__heap__anon(%Handle__heap__anon",
				"define i32 @peek_heap_value(i64",
			},
		},
		{
			name: "json_parser",
			path: filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext"),
			checks: []string{
				"%JsonCursor = type { ptr, i64, i64 }",
				"%JsonLexemeResult = type { i64, i64, i64 }",
				"%JsonNode = type { i32, [2 x i64] }",
				"%JsonParseNodeResult = type { i32, i64 }",
				"define %JsonLexemeResult @json_parse_string_lexeme(ptr",
				"define %JsonLexemeResult @json_parse_number_lexeme(ptr",
				"define i64 @json_parse_string(ptr",
				"define i64 @json_parse_number(ptr",
				"define i64 @json_parse_array(ptr",
				"define i64 @json_parse_object(ptr",
				"define %JsonParseNodeResult @json_parse_value_node(ptr",
				"define %JsonParseNodeResult @json_parse_array_node(ptr",
				"define %JsonParseNodeResult @json_parse_object_node(ptr",
				"define i8 @json_ast_kind(i32 %0, %JsonNode__Store %1)",
				"define %JsonAstArrayIterCursor @json_ast_array_iter_first(i32 %0, %JsonNode__Store %1)",
				"define %JsonAstObjectIterCursor @json_ast_object_iter_first(i32 %0, %JsonNode__Store %1)",
				"define i32 @json_ast_array_nth(i32 %0, i64 %1, %JsonNode__Store %2)",
				"define i32 @json_ast_object_get(i32 %0, ptr %1, ptr %2, %JsonNode__Store %3)",
				"define i64 @json_parser_parity_suite()",
				"define i64 @json_parser_checksum(ptr",
				"define i64 @json_parser_ast_checksum(ptr",
				"define i64 @json_parser_ast_object_len(ptr",
				"define i64 @json_parser_ast_field_string_eq(ptr",
				"define i64 @json_parser_ast_field_kind(ptr",
				"define i64 @json_parser_ast_object_field_len(ptr",
				"define i64 @json_parser_ast_field_number_kind(ptr",
				"define i64 @json_parser_ast_field_string_len(ptr",
				"define i64 @json_parser_ast_field_string_copy(ptr",
				"define i64 @json_parser_ast_field_i64(ptr",
				"define i64 @json_parser_ast_field_u64(ptr",
				"define i64 @json_parser_ast_field_f64(ptr",
				"define i64 @json_parser_ast_field_f32(ptr",
				"define i64 @json_parser_ast_array_field_i64_at(ptr",
				"define i64 @json_parser_ast_array_field_f64_at(ptr",
				"define i64 @json_parser_ast_array_field_f32_at(ptr",
				"define i64 @json_parser_ast_array_field_kind_at(ptr",
				"define i64 @json_parser_ast_array_field_number_kind_at(ptr",
				"define i64 @json_parser_ast_array_field_string_len_at(ptr",
				"define i64 @json_parser_ast_array_field_string_copy_at(ptr",
				"define i64 @json_parser_ast_array_field_string_eq_at(ptr",
				"define i64 @json_parser_ast_array_field_bool_at(ptr",
				"define i64 @json_parser_ast_array_field_is_null_at(ptr",
				"define i64 @json_parser_ast_array_field_array_len_at(ptr",
				"define i64 @json_parser_ast_array_field_object_len_at(ptr",
				"define i64 @json_parser_ast_object_key_eq_at(ptr",
				"define i64 @json_parser_ast_object_key_len_at(ptr",
				"define i64 @json_parser_ast_object_key_copy_at(ptr",
				"define i64 @json_parser_ast_object_field_i64_at(ptr",
				"define i64 @json_parser_ast_object_field_f64_at(ptr",
				"define i64 @json_parser_ast_object_field_f32_at(ptr",
				"define i64 @json_parser_ast_object_value_string_eq_at(ptr",
				"define i64 @json_parser_ast_object_field_bool_at(ptr",
				"define i64 @json_parser_ast_object_field_is_null_at(ptr",
				"define i64 @json_parser_ast_object_value_kind_at(ptr",
				"define i64 @json_parser_ast_object_value_array_len_at(ptr",
				"define i64 @json_parser_ast_object_value_object_len_at(ptr",
				"define i64 @json_parser_ast_object_value_number_kind_at(ptr",
				"define i64 @json_parser_ast_object_value_string_len_at(ptr",
				"define i64 @json_parser_ast_object_value_string_copy_at(ptr",
				"define i64 @json_parser_ast_array_i64_at(ptr",
				"define i64 @json_parser_ast_array_f64_at(ptr",
				"define i64 @json_parser_ast_array_f32_at(ptr",
				"define i64 @json_parser_ast_array_kind_at(ptr",
				"define i64 @json_parser_ast_array_array_len_at(ptr",
				"define i64 @json_parser_ast_array_object_len_at(ptr",
				"define i64 @json_parser_ast_array_number_kind_at(ptr",
				"define i64 @json_parser_ast_array_string_len_at(ptr",
				"define i64 @json_parser_ast_array_string_copy_at(ptr",
				"define i64 @json_parser_ast_array_string_eq_at(ptr",
				"define i64 @json_parser_ast_array_bool_at(ptr",
				"define i64 @json_parser_ast_array_is_null_at(ptr",
				"%JsonParserDocument = type { i64 }",
				"define i64 @json_parser_document_parse(ptr",
				"define i64 @json_parser_document_destroy(i64",
				"define i64 @json_parser_document_checksum(ptr",
				"define i64 @json_parser_document_node_count(ptr",
				"define i64 @json_parser_document_root_kind(ptr",
				"define i64 @json_parser_document_object_len(ptr",
				"define i64 @json_parser_document_array_len(ptr",
				"define i64 @json_parser_document_field_kind(ptr",
				"define i64 @json_parser_document_field_string_copy(ptr",
				"define i64 @json_parser_document_field_i64(ptr",
				"define i64 @json_parser_document_field_f64(ptr",
				"define i64 @json_parser_document_field_f32(ptr",
				"define i64 @json_parser_document_array_field_i64_at(ptr",
				"define i64 @json_parser_document_array_field_f64_at(ptr",
				"define i64 @json_parser_document_array_field_f32_at(ptr",
				"define i64 @json_parser_document_object_key_copy_at(ptr",
				"define i64 @json_parser_document_object_value_string_copy_at(ptr",
				"define i64 @json_parser_document_object_field_f64_at(ptr",
				"define i64 @json_parser_document_object_field_f32_at(ptr",
				"define i64 @json_parser_document_array_string_copy_at(ptr",
				"define i64 @json_parser_document_array_f64_at(ptr",
				"define i64 @json_parser_document_array_f32_at(ptr",
				"%JsonParserValue = type { i64, ptr }",
				"define i64 @json_parser_document_root_value(ptr",
				"define i64 @json_parser_value_is_valid(ptr",
				"define i64 @json_parser_value_kind(ptr",
				"define i64 @json_parser_value_object_len(ptr",
				"define i64 @json_parser_value_array_len(ptr",
				"define i64 @json_parser_value_field(ptr",
				"define i64 @json_parser_value_index(ptr",
				"define i64 @json_parser_value_object_key_at(ptr",
				"define i64 @json_parser_value_object_value_at(ptr",
				"define i64 @json_parser_value_string_eq(ptr",
				"define i64 @json_parser_value_field_kind(ptr",
				"define i64 @json_parser_value_field_string_eq(ptr",
				"define i64 @json_parser_value_field_i64(ptr",
				"define i64 @json_parser_value_field_f64(ptr",
				"define i64 @json_parser_value_field_array_len(ptr",
				"define i64 @json_parser_value_index_kind(ptr",
				"define i64 @json_parser_value_index_string_eq(ptr",
				"define i64 @json_parser_value_index_i64(ptr",
				"define i64 @json_parser_value_index_f64(ptr",
				"define i64 @json_parser_value_index_array_len(ptr",
				"define i64 @json_parser_value_index_object_len(ptr",
				"define i64 @json_parser_value_string_copy(ptr",
				"define i64 @json_parser_value_i64(ptr",
				"define i64 @json_parser_value_u64(ptr",
				"define i64 @json_parser_value_f64(ptr",
				"define i64 @json_parser_value_f32(ptr",
				"%JsonParserArrayIter = type { i64, ptr, i64, i64 }",
				"%JsonParserObjectIter = type { i64, ptr, i64, i64 }",
				"define i64 @json_parser_value_array_iter(ptr",
				"define i64 @json_parser_value_object_iter(ptr",
				"define i64 @json_parser_array_iter_is_valid(ptr",
				"define i64 @json_parser_array_iter_kind(ptr",
				"define i64 @json_parser_array_iter_string_eq(ptr",
				"define i64 @json_parser_array_iter_i64(ptr",
				"define i64 @json_parser_array_iter_f64(ptr",
				"define i64 @json_parser_array_iter_array_len(ptr",
				"define i64 @json_parser_array_iter_object_len(ptr",
				"define i64 @json_parser_array_iter_value(ptr",
				"define i64 @json_parser_array_iter_next(ptr",
				"define i64 @json_parser_object_iter_is_valid(ptr",
				"define i64 @json_parser_object_iter_key_eq(ptr",
				"define i64 @json_parser_object_iter_field_kind(ptr",
				"define i64 @json_parser_object_iter_field_string_eq(ptr",
				"define i64 @json_parser_object_iter_field_f64(ptr",
				"define i64 @json_parser_object_iter_field_array_len(ptr",
				"define i64 @json_parser_object_iter_key_string_eq(ptr",
				"define i64 @json_parser_object_iter_value_kind(ptr",
				"define i64 @json_parser_object_iter_value_string_eq(ptr",
				"define i64 @json_parser_object_iter_value_f64(ptr",
				"define i64 @json_parser_object_iter_value_array_len(ptr",
				"define i64 @json_parser_object_iter_value_object_len(ptr",
				"define i64 @json_parser_object_iter_key(ptr",
				"define i64 @json_parser_object_iter_value(ptr",
				"define i64 @json_parser_object_iter_next(ptr",
				"define i64 @json_parallel_worker(%JsonParallelJob",
				"define i64 @json_parser_parallel_max_workers()",
				"define i64 @json_parser_parallel_checksum(",
				"define i64 @json_parser_parallel_ast_checksum(",
				"call %ThreadPool @pool_new(",
				"call void @task_group_wait_all_raw(ptr",
			},
		},
		{
			name: "grammar_surface_precedence",
			path: filepath.Join(repoRoot, "Code", "test_programs", "grammar_surface_precedence.llcontext"),
			checks: []string{
				"%Token = type { i32 }",
				"define %Token @grammar_surface_parse_expr(ptr",
				"define %Token @grammar_surface_parse_one_off(ptr",
				"@__grammar_try__Arithmetic____grammar_precedence_Arithmetic_expression_1_compare(",
				"@__grammar_try__Arithmetic____grammar_precedence_Arithmetic_one_off_expression_1_inline(",
			},
		},
		{
			name: "grammar_uses_shared_helpers",
			path: filepath.Join(repoRoot, "Code", "test_programs", "grammar_uses_shared_helpers.llcontext"),
			checks: []string{
				"%Token = type { i32 }",
				"define %Token @grammar_uses_parse_statement(ptr",
				"define %Token @grammar_uses_recovering_atom(ptr",
				"@__grammar_try__StatementGrammar____grammar_precedence_StatementGrammar_statement_1_compare(",
				"call void @record_parse_error(ptr",
				"@__grammar_try__StatementGrammar__recovering_atom(",
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
			name: "char_literals",
			path: filepath.Join(repoRoot, "Code", "test_programs", "char_literals.llcontext"),
			checks: []string{
				"define i64 @char_code()",
				"define i64 @escaped_char_code()",
				"define i1 @char_compare()",
				"define i64 @char_array_checksum()",
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

func TestReadSourceWithIncludesPreservesIncludeBoundariesWithoutTrailingNewlines(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.llcontext")
	midPath := filepath.Join(dir, "mid.llcontext")
	rootPath := filepath.Join(dir, "root.llcontext")

	if err := os.WriteFile(leafPath, []byte("leaf_line"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(midPath, []byte("# include \"leaf.llcontext\"\nmid_line"), 0o644); err != nil {
		t.Fatalf("write mid fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("root_start\n# include \"mid.llcontext\"\nroot_end"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}

	got := string(expanded)
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
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

func TestRunCLIEmitsModuleInterface(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_interface_fixture.llcontext")
	interfacePath := filepath.Join(fixtureDir, "module_interface_fixture.llcontexti")
	src := "struct Box[T]:\n    value: T\n\nglobal counter: int = 0\n\ndef identity[T](value: T) -> T:\n    return value\n\nnamespace util:\n    def inc(value: int) -> int:\n        return value + 1\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write interface fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "iface", "-o", interfacePath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interface emit to succeed, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected interface emit with -o not to write stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	data, err := os.ReadFile(interfacePath)
	if err != nil {
		t.Fatalf("expected interface output file %s to exist: %v", interfacePath, err)
	}
	interfaceSource := string(data)
	for _, check := range []string{
		"struct Box[T]:",
		"extern counter: int",
		"extern identity[T](value: T) -> T",
		"namespace util:",
		"extern inc(value: int) -> int",
	} {
		if !strings.Contains(interfaceSource, check) {
			t.Fatalf("expected interface source to contain %q, got:\n%s", check, interfaceSource)
		}
	}
	for _, bad := range []string{"return value", "global counter: int = 0"} {
		if strings.Contains(interfaceSource, bad) {
			t.Fatalf("expected interface source to omit %q, got:\n%s", bad, interfaceSource)
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "ast", interfacePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected generated interface to parse successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected generated interface parse to be warning-free, got:\n%s", stderr.String())
	}
	for _, check := range []string{"extern identity[T](1 params) -> T", "extern counter: int"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected generated interface AST to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIEmitsSourceDependenciesJSON(t *testing.T) {
	fixtureDir := t.TempDir()
	leafPath := filepath.Join(fixtureDir, "leaf.llcontext")
	midPath := filepath.Join(fixtureDir, "mid.llcontext")
	rootPath := filepath.Join(fixtureDir, "root.llcontext")
	for path, content := range map[string]string{
		leafPath: "def leaf() -> int:\n    return 1\n",
		midPath:  "# include \"leaf.llcontext\"\n\ndef mid() -> int:\n    return leaf()\n",
		rootPath: "# include \"mid.llcontext\"\n\ndef main() -> int:\n    return mid()\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "deps-json", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deps-json emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	var report sourceDependencyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected deps-json output to decode: %v\n%s", err, stdout.String())
	}
	rootAbs, _ := filepath.Abs(rootPath)
	midAbs, _ := filepath.Abs(midPath)
	leafAbs, _ := filepath.Abs(leafPath)
	if report.Root != rootAbs {
		t.Fatalf("expected root dependency %s, got %s", rootAbs, report.Root)
	}
	want := []string{rootAbs, midAbs, leafAbs}
	if len(report.Files) != len(want) {
		t.Fatalf("expected %d dependencies, got %d (%v)", len(want), len(report.Files), report.Files)
	}
	for i, got := range report.Files {
		if got != want[i] {
			t.Fatalf("dependency %d mismatch: got %s want %s", i, got, want[i])
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

func TestParseArgsRejectsRemovedPackedABI(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "equals", args: []string{"-packed-abi=word-handle", "fixture.llcontext"}},
		{name: "separate", args: []string{"-packed-abi", "row-handle", "fixture.llcontext"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := parseArgs(test.args)
			if err == nil {
				t.Fatal("expected removed packed ABI flag error, got none")
			}
			if !strings.Contains(err.Error(), "-packed-abi has been removed") {
				t.Fatalf("expected removed packed ABI diagnostic, got %q", err.Error())
			}
		})
	}
}

func TestParseArgsDefaultsPackedLoweringToCanonicalProfile(t *testing.T) {
	options, err := parseArgs([]string{"fixture.llcontext"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.packedProfile.Contract() != backend.PackedLoweringContractCanonicalCompilerGraph {
		t.Fatalf("expected canonical packed lowering profile by default, got %q", options.packedProfile.Contract())
	}
	if options.packedProfile.SelectionKey() != "canonical" {
		t.Fatalf("expected canonical packed lowering selection by default, got %q", options.packedProfile.SelectionKey())
	}
}

func TestResolveProjectTargetRejectsRemovedPackedABI(t *testing.T) {
	projectRoot := t.TempDir()
	project := &resolvedProject{
		root:     projectRoot,
		filePath: filepath.Join(projectRoot, projectFileName),
		config: projectDefinition{
			Targets: map[string]projectTargetDefinition{
				"default": {
					Entry:     "main.llcontext",
					PackedABI: "row-handle",
				},
			},
		},
	}

	_, err := resolveProjectTarget(project, projectCLIOptions{})
	if err == nil {
		t.Fatal("expected removed project packed-abi diagnostic, got none")
	}
	if !strings.Contains(err.Error(), "uses removed packed-abi override") {
		t.Fatalf("expected removed project packed-abi diagnostic, got %q", err.Error())
	}
}

func TestParseArgsAcceptsPackedInspectEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "packed-info", "fixture.llcontext"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitPacked {
		t.Fatalf("expected packed emit mode, got %q", options.emit)
	}
}

func TestParseArgsAcceptsLoweredEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "lower", "fixture.llcontext"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitLowered {
		t.Fatalf("expected lowered emit mode, got %q", options.emit)
	}
}

func TestParseArgsAcceptsSemanticEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "sema", "fixture.llcontext"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitSemantic {
		t.Fatalf("expected semantic emit mode, got %q", options.emit)
	}
}

func TestRunCLIWritesLoweredGrammarSourceToDefaultPath(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "lowered_fixture.llcontext")
	src := "grammar PascalFrontend:\n    expression(state: mutable ParserState&) -> Token:\n        token(TokenKind.IDENT)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write lowered fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "lowered", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	outputPath := filepath.Join(fixtureDir, "lowered_fixture"+loweredExtension)
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read lowered output file: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		"grammar PascalFrontend:",
		"def expression(state: mutable ParserState&) -> Token:",
		"state.expect_kind(TokenKind.IDENT)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRunCLIEmitsSemanticReport(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "semantic_fixture.llcontext")
	src := "const enum TokenKind of u32:\n    IDENT = 1\n\nstruct Token:\n    kind: TokenKind\n\nstruct ParserState:\n    cursor: mutable usize\n\nimpl mutable ParserState&:\n    def expect_kind(self: mutable ParserState&, kind: TokenKind) -> Token:\n        _ = kind\n        return Token{kind: TokenKind.IDENT}\n\ngrammar PascalFrontend:\n    expression(state: mutable ParserState&) -> Token:\n        token(TokenKind.IDENT)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write semantic fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"=== lowered ===",
		"def expression(state: mutable ParserState&) -> Token:",
		"=== semantic ===",
		"func expression",
		"signature: func(mutable ParserState&) -> Token",
		"func __grammar_try__PascalFrontend__expression",
		"return_isolation:",
		"fact_snapshot:",
		"fact_groups:",
		"fact_blocks:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected semantic report to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRunCLICompilesJSONParserWithEnumDenseFixedOverrideByDefault(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")

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
		"%JsonParseNodeResult = type { i32, i64 }",
		"define %JsonParseNodeResult @json_parse_value_node(ptr",
		"define %JsonParseNodeResult @json_parse_array_node(ptr",
		"define %JsonParseNodeResult @json_parse_object_node(ptr",
		"call void @ctx_packed_store_reserve(",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(",
		"call i32 @ctx_packed_store_read_index_tag(ptr %packed.tag.store.state, i32 ",
		"call i64 @ctx_packed_store_read_index_word(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_variant_sparse_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state, i32 ",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_tagged_variant_sparse_result(ptr %packed.alloc.store.arena, i64 ",
		"call i32 @ctx_packed_store_read_variant_sparse_tag(ptr %packed.tag.store.state, i32 ",
		"call i64 @ctx_packed_store_read_variant_sparse_word(i32 %node2, ptr %packed.payload.word.state, i64 ",
		"call i64 @ctx_packed_store_read_word(ptr %packed.payload.word.arena",
		"call i64 @ctx_packed_store_read_word(ptr %packed.common.store.arena",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
		"call ptr @ctx_packed_store_decode_index(ptr %packed.decode.store.arena, i32",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state, i32 ",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr %packed.alloc.store.arena, i64 %packed.alloc.bytes, ptr %packed.alloc.store.state)",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected enum-level dense-fixed lowering default to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestRunCLIPrintsPackedLoweringSummary(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "packed_info.llcontext")
	src := "@packed_profile(build_heavy)\npacked enum Expr:\n    common:\n        @storage(side_table)\n        span: i64\n        kind: u32\n    Lit(value: i64)\n    End\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write packed info fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "packed", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"packed lowering",
		"contract: canonical-compiler-graph",
		"Expr",
		"effective abi: index-soa",
		"profile: build-heavy",
		"declared abi override: dense-fixed",
		"declared prefix override: common-only",
		"side-table common words: 1",
		"- span: i64 side_table word_offset=0 words=1",
		"- kind: u32 inline row_field=1",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected packed summary to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIRejectsRemovedPackedABIFlag(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", "-packed-abi", "word-handle", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatal("expected removed packed ABI flag to fail")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-packed-abi has been removed") {
		t.Fatalf("expected removed packed ABI diagnostic on stderr, got:\n%s", stderr.String())
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
		// This test validates generated-header ABI wiring, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
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
		// This test validates generated-header ABI wiring, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
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

func TestRunCLIJSONParserGeneratedHeaderInteropBuildSmoke(t *testing.T) {
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
		// This test validates generated-header ABI wiring, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
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
}

func TestRunCLIJSONParserParallelBenchBuildSmoke(t *testing.T) {
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

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates benchmark wiring and smoke behavior, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
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
}

func runPackedMLASTBenchSmoke(t *testing.T, exePath string) {
	t.Helper()

	for _, tc := range []struct {
		name     string
		args     []string
		contains []string
	}{
		{name: "scalar", args: []string{"scalar", "3"}, contains: []string{"mode=scalar", "iterations=3", "workers=1", "checksum=", "total_checksum=", "seconds="}},
		{name: "parallel", args: []string{"parallel", "4", "2"}, contains: []string{"mode=parallel", "iterations=4", "workers=2", "checksum=", "total_checksum=", "seconds="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runCmd := exec.Command(exePath, tc.args...)
			runOutput, err := runCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("packed ML AST benchmark failed for %s: %v\n%s", tc.name, err, string(runOutput))
			}
			output := string(runOutput)
			for _, check := range tc.contains {
				if !strings.Contains(output, check) {
					t.Fatalf("expected packed ML AST benchmark output to contain %q, got:\n%s", check, output)
				}
			}
		})
	}
}

func TestRunCLIPackedMLASTBenchSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTMediumNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}

func TestRunCLIPackedMLASTMegaBenchSmoke(t *testing.T) {
	requireSlowNativeMLAST(t)

	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}

func TestRunCLIPackedMLASTUltraBenchSmoke(t *testing.T) {
	requireSlowNativeMLAST(t)

	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTUltraNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}

func TestRunCLIPackedMLExprReproSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLExprReproExecutable(t, repoRoot, "-O0")

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("packed ML expr repro failed: %v\n%s", err, string(runOutput))
	}
	output := strings.TrimSpace(string(runOutput))
	if output == "" {
		t.Fatal("expected packed ML expr repro to print a checksum")
	}
	if _, err := strconv.ParseInt(output, 10, 64); err != nil {
		t.Fatalf("expected packed ML expr repro to print an integer checksum, got %q", output)
	}
}

func TestRunCLIJSONParserDOMBenchSmoke(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")
	benchPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_dom_bench.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	runtimePath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_dom_bench")
	jsonPath := filepath.Join(outputDir, "sample.json")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates benchmark wiring and smoke behavior, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
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

	if err := os.WriteFile(jsonPath, []byte("{\"items\":[1,2,3,{\"ok\":true}],\"meta\":{\"name\":\"Ada\",\"pi\":3.14},\"none\":null}\n"), 0o644); err != nil {
		t.Fatalf("failed to write sample json: %v", err)
	}

	for _, tc := range []struct {
		name     string
		mode     string
		contains []string
	}{
		{name: "default-parse", mode: "", contains: []string{"mode=dom-parse", "iterations=4", "parses=4", "MiB/s="}},
		{name: "build", mode: "build", contains: []string{"mode=dom-build", "iterations=4", "parses=4", "MiB/s="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{jsonPath, "4"}
			if tc.mode != "" {
				args = append(args, tc.mode)
			}
			runCmd := exec.Command(exePath, args...)
			runOutput, err := runCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dom json benchmark failed: %v\n%s", err, string(runOutput))
			}
			output := string(runOutput)
			for _, check := range tc.contains {
				if !strings.Contains(output, check) {
					t.Fatalf("expected dom benchmark output to contain %q, got:\n%s", check, output)
				}
			}
		})
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
		"[ RUN      ] ast_number_helpers_materialize_float_values_across_fractional_and_large_inputs",
		"[ RUN      ] ast_iterator_helpers_walk_object_fields_and_array_items",
		"[ SUMMARY  ] 9 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected json parser self-hosted test output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesStage1RuntimeToLLVM(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "llcontext_std", "contextlang_runtime.llcontext")

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
		"%FixedBufferAllocator = type { ptr, i64, i64 }",
		"define i64 @ctx_string_view_len(%StringView",
		"define %FixedBufferAllocator @fixed_buffer_allocator_init(",
		"define ptr @fixed_buffer_alloc(",
		"define void @fixed_buffer_reset(",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr",
		"define void @ctx_packed_store_alloc_result_slow(ptr",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(ptr",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(ptr",
		"define void @ctx_packed_store_alloc_fixed_result_slow(ptr",
		"define void @ctx_packed_store_reserve(ptr",
		"define %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(ptr",
		"%DynDict__dstr_key_shape__i64 = type { ptr, i64, i64, i64, ptr }",
		"define %DynDict__dstr_key_shape__i64 @arena_dict_new__i64(",
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
	if err := os.WriteFile(fixturePath, []byte("def bad() -> u8&:\n    return \"oops\\q\" -> u8&\n"), 0o644); err != nil {
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

func TestRunCLIRejectsInvalidCharLiteral(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_char.llcontext")
	if err := os.WriteFile(fixturePath, []byte("def bad() -> i64:\n    return '\\u0080'.i64()\n"), 0o644); err != nil {
		t.Fatalf("failed to write invalid char fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "char literal must decode to exactly one code unit") {
		t.Fatalf("expected invalid char diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsGenericKeyRuntimeBackedDictSugar(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "generic_key_dict_runtime_reject.llcontext")
	src := "def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> mutable T&?:\n    return null\n\ndef bad(values: dict[u32, i64], key: u32) -> mutable i64&?:\n    return values.get(key)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write generic-key dict runtime rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "runtime-backed dict operations currently support only dict[dstr, V]") {
		t.Fatalf("expected generic-key runtime-backed dict diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIExecutesCharLiteralSmokeProgram(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "char_literals.llcontext")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "char_literals.o")
	exePath := filepath.Join(outputDir, "char_literals")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected char literal smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling char literal smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling char literal smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
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
		t.Fatalf("char literal smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected char literal smoke program to produce no output, got:\n%s", string(runOutput))
	}
}

func TestRunCLIExecutesAllocatorPortSmokeProgram(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "allocator_ports.llcontext")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "allocator_ports.o")
	exePath := filepath.Join(outputDir, "allocator_ports")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected allocator port smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling allocator port smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling allocator port smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
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
		t.Fatalf("allocator port smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected allocator port smoke program to produce no output, got:\n%s", string(runOutput))
	}
}

func TestRunCLIExecutesDequePortSmokeProgram(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "deque_ports.llcontext")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "deque_ports.o")
	exePath := filepath.Join(outputDir, "deque_ports")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deque port smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling deque port smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling deque port smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
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
		t.Fatalf("deque port smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected deque port smoke program to produce no output, got:\n%s", string(runOutput))
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
	src := "struct Holder:\n    value: i32&\n\nstruct Window:\n    items: view[Holder]\n\n@borrows_return(window.items[*])\nextern borrow_value(window: Window) -> view[Holder]\n"
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
	for _, check := range []string{"struct Holder (1 fields)", "struct Window (1 fields)"} {
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
	src := "const enum JsonNodeKind of i8:\n    Invalid = -1\n    Null\n    Bool = 1\n    String\n\nconst DEFAULT_KIND: JsonNodeKind = JsonNodeKind.String\n\ndef kind_raw(kind: JsonNodeKind) -> i8:\n    return kind.i8()\n\ndef is_string(kind: JsonNodeKind) -> bool:\n    return kind == JsonNodeKind.String\n\ndef default_kind() -> JsonNodeKind:\n    return DEFAULT_KIND\n\ndef make_kind() -> JsonNodeKind:\n    return 1i8 -> JsonNodeKind\n"
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

func TestRunCLIRejectsLegacyCastSyntax(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_cast_error.llcontext")
	src := "const VALUE: i64 = 1.cast[i64]()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy cast fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy cast syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy cast syntax `.cast[T]()` is no longer supported") {
		t.Fatalf("expected legacy cast parser error on stderr, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsLegacyReverseIterableLoopSyntax(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_reverse_iter_error.llcontext")
	src := "def walk(items: darray[int]) -> void:\n    for rev value in items:\n        pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy reverse iterable fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy reverse iterable syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported") {
		t.Fatalf("expected legacy reverse iterable parser error on stderr, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no formatter output on parser failure, got:\n%s", stdout.String())
	}
}

func TestRunCLIRejectsLegacyReprCStructSyntax(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_repr_c_struct_error.llcontext")
	src := "repr(c) struct Holder:\n    value: i32&\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy repr(c) fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy repr(c) syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy `repr(c) struct` syntax is no longer supported") {
		t.Fatalf("expected legacy repr(c) parser error on stderr, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsInternalRuntimeCarrierTypes(t *testing.T) {
	prev, hadPrev := os.LookupEnv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Unsetenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
	defer func() {
		if hadPrev {
			_ = os.Setenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS", prev)
		} else {
			_ = os.Unsetenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
		}
	}()

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "runtime_carrier_warning.llcontext")
	src := "extern take_view(view: StringView) -> void\nextern take_raw[T](values: DynArray[T]) -> void\nextern take_window(view: DynArrayView) -> void\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write runtime carrier rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interface", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for runtime carrier types, stdout:\n%s", stdout.String())
	}
	for _, want := range []string{
		`internal runtime carrier type "StringView" is not supported in user-facing code; use "sview[...]" instead`,
		`internal runtime carrier type "DynArray" is not supported in user-facing code; use "darray[T, shape]" instead`,
		`internal runtime carrier type "DynArrayView" is not supported in user-facing code; use "dview[T]" instead`,
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected runtime carrier rejection %q, got:\n%s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no AST output on runtime carrier rejection, got:\n%s", stdout.String())
	}
}

func TestRunCLIFmtNormalizesSingleStatementGrantBlocks(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_single_use.llcontext")
	src := "def write_once(text: u8&) -> int:\n    can Console.Write:\n        return puts(text)\n\ndef assign_once(target: mutable i64&):\n    can Memory.Allocate:\n        target <- alloc_value()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write single-use grant fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	for _, check := range []string{
		"return puts(text) can Console.Write",
		"target <- alloc_value() can Memory.Allocate",
	} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}
	for _, forbidden := range []string{"can Console.Write:", "can Memory.Allocate:"} {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("expected formatter to inline %q, got:\n%s", forbidden, formatted)
		}
	}
}

func TestRunCLIFmtKeepsPanicGrantBlocksInSurfaceSyntax(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_panic.llcontext")
	src := "def boom():\n    can Abort.Panic:\n        panic(\"boom\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write panic grant fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	if !strings.Contains(formatted, "can Abort.Panic:\n        panic(\"boom\")") {
		t.Fatalf("expected formatter to preserve the panic grant block, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "can can[") || strings.Contains(formatted, "panic(\"boom\") can Abort.Panic") {
		t.Fatalf("expected formatter to keep surface grant syntax for panic blocks, got:\n%s", formatted)
	}
}

func TestRunCLIFmtKeepsSignalSurfaceSyntax(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_signal.llcontext")
	src := "effect FooEffect:\n    pass\n\neffect ConsoleEffect:\n    Write\n\ndef run() -> void:\n    can FooEffect, ConsoleEffect.Write:\n        signal FooEffect\n        signal ConsoleEffect.Write\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write signal grant fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	for _, check := range []string{"signal FooEffect", "signal ConsoleEffect.Write"} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}
	if strings.Contains(formatted, "signal can[") {
		t.Fatalf("expected signal statements to stay in surface syntax, got:\n%s", formatted)
	}
	if err := os.WriteFile(fixturePath, []byte(formatted), 0o644); err != nil {
		t.Fatalf("failed to rewrite signal fixture with formatted output: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted signal output to round-trip, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on signal round-trip, got:\n%s", stderr.String())
	}
}

func TestRunCLIFmtRoundTripsTryReturnGrantBlocks(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_try_return.llcontext")
	src := "error FormatError:\n    WriteFailed\n\nextern checked() -> int error[FormatError] can[Console.Format]\n\ndef run() -> int:\n    can Console.Format:\n        return try checked() else 1\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write try-return grant fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	if !strings.Contains(formatted, "return (try checked() else 1) can Console.Format") {
		t.Fatalf("expected formatter to inline try-return grant block, got:\n%s", formatted)
	}
	if err := os.WriteFile(fixturePath, []byte(formatted), 0o644); err != nil {
		t.Fatalf("failed to rewrite try-return fixture with formatted output: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted try-return output to round-trip, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on try-return round-trip, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "return (try checked() else 1) can Console.Format") {
		t.Fatalf("expected round-tripped formatter output to preserve inlined try-return grant, got:\n%s", stdout.String())
	}
}

func TestRunCLIPrintsPostfixCastHookSyntaxInAST(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_ast.llcontext")
	src := "const VALUE: i64 = 1.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast hook AST fixture: %v", err)
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
	for _, check := range []string{"const VALUE = 1.i64()"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesBuiltinPostfixCastWithoutHook(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "builtin_postfix_cast_llvm.llcontext")
	src := "def via_postfix() -> i64:\n    return 21.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write builtin postfix cast fixture: %v", err)
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
	if !strings.Contains(output, "define i64 @via_postfix(") {
		t.Fatalf("expected LLVM output to contain via_postfix definition, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @cast__") {
		t.Fatalf("expected builtin postfix cast to avoid __cast__ hook lowering, got:\n%s", output)
	}
}

func TestRunCLICompilesPostfixCastHookToHookCall(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_llvm.llcontext")
	src := "enum Op:\n    Add\n    Sub\n\ndef __cast__(op: Op) -> i64:\n    if op == Op.Add:\n        return 10\n    return 20\n\ndef via_postfix(op: Op) -> i64:\n    return op.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast hook LLVM fixture: %v", err)
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
	for _, check := range []string{
		"define i64 @cast__Op__to__i64__L5_C1(",
		"define i64 @via_postfix(",
		"call i64 @cast__Op__to__i64__L5_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesMultiplePostfixCastHooksInOneFile(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "multiple_postfix_cast_hooks.llcontext")
	src := "const enum LuaUnaryOp of i8:\n    NEGATE = 0\n    NOT = 1\n\nconst enum LuaBinaryOp of i8:\n    ADD = 0\n    SUB = 1\n\ndef __cast__(op: LuaBinaryOp) -> i64:\n    if op == LuaBinaryOp.ADD:\n        return 3\n    return op.cast[i64] + 5\n\ndef __cast__(op: LuaUnaryOp) -> i64:\n    if op == LuaUnaryOp.NEGATE:\n        return 29\n    return 31\n\ndef binary_score(op: LuaBinaryOp) -> i64:\n    return op.i64()\n\ndef unary_score(op: LuaUnaryOp) -> i64:\n    return op.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write multi-hook fixture: %v", err)
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
	for _, check := range []string{
		"define i64 @cast__LuaBinaryOp__to__i64__L9_C1(",
		"define i64 @cast__LuaUnaryOp__to__i64__L14_C1(",
		"call i64 @cast__LuaBinaryOp__to__i64__L9_C1(",
		"call i64 @cast__LuaUnaryOp__to__i64__L14_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIRejectsArrowCastWhenOnlyPostfixHookExists(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_reject_arrow.llcontext")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef bad(op: Op) -> i64:\n    return op -> i64\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid cast from Op to i64") {
		t.Fatalf("expected explicit cast diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsDotCastSyntaxWhenOnlyPostfixHookExists(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_reject_dot_cast.llcontext")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef bad(op: Op) -> i64:\n    return op.cast[i64]\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write dot-cast rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid cast from Op to i64") {
		t.Fatalf("expected explicit .cast[T] diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsDuplicatePostfixCastHooksForSamePair(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "duplicate_postfix_cast_hooks.llcontext")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef __cast__(op: Op) -> i64:\n    return 20\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write duplicate cast-hook fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "duplicate __cast__ hook for Op -> i64") {
		t.Fatalf("expected duplicate cast-hook diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsPostfixCastHookWithWrongArity(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_postfix_cast_hook_arity.llcontext")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op, extra: i64) -> i64:\n    return extra\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write invalid cast-hook fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "must take exactly 1 parameter, got 2") {
		t.Fatalf("expected cast-hook arity diagnostic, got:\n%s", stderr.String())
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
		{
			name:     "benches filtered",
			args:     []string{"-emit", "benches", "-filter", "hot", fixturePath},
			contains: []string{"bench_hot_loop\tfunc() -> void"},
			omits:    []string{"alpha_case", "shared_seed"},
		},
		{
			name:     "fixtures filtered",
			args:     []string{"-emit", "fixtures", "-filter", "seed", fixturePath},
			contains: []string{"shared_seed\tfunc() -> int"},
			omits:    []string{"alpha_case", "bench_hot_loop"},
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

func TestRunCLIFormatsSourceCanonically(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "format_fixture.llcontext")
	src := "@test\ndef sample_case(value: i64) -> i64:\n    values=[1,2,3]\n    if likely value > 0:\n        return (value)\n    return 0\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write format fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	for _, check := range []string{
		"@test\n",
		"def sample_case(value: i64) -> i64:",
		"values = [1, 2, 3]",
		"if likely (value > 0):",
	} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}

	formattedPath := filepath.Join(fixtureDir, "formatted_fixture.llcontext")
	if err := os.WriteFile(formattedPath, stdout.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write formatted fixture: %v", err)
	}
	var astStdout bytes.Buffer
	var astStderr bytes.Buffer
	exitCode = runCLI([]string{"-emit", "ast", formattedPath}, &astStdout, &astStderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted source to reparse successfully, stderr:\n%s", astStderr.String())
	}
	if astStderr.Len() != 0 {
		t.Fatalf("expected reparsed formatted source to stay warning-free, got:\n%s", astStderr.String())
	}
}

func TestRunCLIEmitsReferenceDocs(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "reference_fixture.llcontext")
	src := "struct Pair:\n    left: i64\n    right: i64\n\n@test\ndef build_pair(value: i64) -> Pair:\n    return Pair(value, value)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write reference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "doc", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected doc generation to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"# Reference: reference_fixture.llcontext",
		"## Struct `Pair`",
		"- declaration: `struct Pair:`",
		"- fields:",
		"`left: i64`",
		"## Function `build_pair`",
		"- declaration: `def build_pair(value: i64) -> Pair:`",
		"- annotations:",
		"`@test`",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected reference docs to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIGeneratesSkippedTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "skipped_test_runner_fixture.llcontext")
	src := "@skip(todo)\n@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write skipped runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to generate skipped test runner, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[ SKIPPED  ] alpha_case (todo)") {
		t.Fatalf("expected skipped test runner to mention alpha_case skip, got:\n%s", output)
	}
	if strings.Contains(output, "\talpha_case()\n") {
		t.Fatalf("expected skipped runner not to invoke alpha_case, got:\n%s", output)
	}
	if !strings.Contains(output, "\tbeta_case()\n") {
		t.Fatalf("expected skipped runner to invoke beta_case, got:\n%s", output)
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

func TestRunCLIExecutesEffectfulSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_effectful_tests_fixture.llcontext")
	src := "@test\ndef memory_case() -> void:\n    can Memory.Allocate, Abort.Panic:\n        values: i64[4] = zeroed\n        values[0] <- 7\n        if values[0] != 7:\n            panic(\"expected initialized value\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write effectful execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected effectful test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] memory_case",
		"[       OK ] memory_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected effectful test execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesPoolBackedSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_pool_tests_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "Code", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

struct WriteJob:
	slot_bits: uintptr
	value: i64

def write_slot(job: WriteJob) -> i64:
	slot: mutable i64& = job.slot_bits.cast[mutable i64&]
	slot[0] <- job.value
	return job.value

@test
def pool_backed_case() -> void:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
		partials: i64[2] = zeroed
		pool workers(2):
			group: mutable TaskGroup = task_group_new()
			first_bits: uintptr = (&partials[0]).cast[i64&].uintptr()
			second_bits: uintptr = (&partials[1]).cast[i64&].uintptr()
			first: Task[i64, Pending] = submit write_slot(WriteJob(first_bits, 1))
			second: Task[i64, Pending] = submit write_slot(WriteJob(second_bits, 2))
			task_group_add((&group).cast[TaskGroup&], move first)
			task_group_add((&group).cast[TaskGroup&], move second)
			wait all group
		assert_eq(partials[0], 1)
		assert_eq(partials[1], 2)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write pool execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected pool-backed test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] pool_backed_case",
		"[       OK ] pool_backed_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected pool-backed execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIAcceptsBareSViewLocalAnnotationInObjectBuild(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "sview_local_obj_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "Code", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def local_view(src: u8&) -> i64:
	text: sview = string_view(src, 0, 1)
	return text.len
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write sview local object fixture: %v", err)
	}

	objectPath := filepath.Join(t.TempDir(), "sview_local_obj_fixture.o")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected bare sview local annotation object build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("expected object file to be produced: %v", err)
	}
}

func TestRunCLIAcceptsSurfaceRuntimeBackedLocalAnnotationsInObjectBuild(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "surface_runtime_locals_obj_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "Code", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

extern make_text() -> dstr
extern make_bytes() -> darray[u8]
extern make_window() -> dview[u8]
extern make_table() -> dict[dstr, i64]

def local_runtime_locals() -> usize:
	text: dstr = make_text()
	bytes: darray[u8] = make_bytes()
	window: dview[u8] = make_window()
	table: dict[dstr, i64] = make_table()
	return text.len + bytes.count + window.len + table.count
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write surface-runtime locals object fixture: %v", err)
	}

	objectPath := filepath.Join(t.TempDir(), "surface_runtime_locals_obj_fixture.o")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected surface runtime-backed local annotations object build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("expected object file to be produced: %v", err)
	}
}

func TestRunCLIEmitsSelectedTestPhaseDebug(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	t.Setenv("LLCONTEXT_TEST_PHASE_DEBUG", "1")

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_tests_phase_debug_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write phase-debug execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected phase-debug test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[ phase    ] emit_test selected_test_execution",
		"[ phase    ] selected_tests read_source",
		"[ phase    ] selected_tests select_cases",
		"[ phase    ] selected_tests compile_dispatch",
		"[ phase    ] selected_tests run_cases",
	} {
		if !strings.Contains(stderr.String(), check) {
			t.Fatalf("expected selected-test phase debug output to contain %q, got:\n%s", check, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), "[       OK ] alpha_case") {
		t.Fatalf("expected successful test output, got:\n%s", stdout.String())
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

func TestRunCLIExecutesSelectedTestsWithGlobFilter(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_glob_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write glob execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "*beta*", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected glob-filtered test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "alpha_case") {
		t.Fatalf("expected glob-filtered execution not to mention alpha_case, got:\n%s", output)
	}
	for _, check := range []string{
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected glob-filtered execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIContinuesAfterFailingAndSkippedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_fail_skip_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    can Abort.Panic:\n        panic(\"boom\")\n\n@skip(todo)\n@test\ndef beta_case() -> void:\n    pass\n\n@test\ndef gamma_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fail/skip execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected fail/skip test execution to return non-zero, stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected harness stderr to stay empty, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"PANIC",
		"[ ACTIVE   ] alpha_case",
		"alpha_case",
		"panic at ",
		"backtrace:",
		"[ SKIPPED  ] beta_case (todo)",
		"[ RUN      ] gamma_case",
		"[       OK ] gamma_case",
		"[ SUMMARY  ] 3 test(s) selected; passed=1 skipped=1 failed=1",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected richer test harness output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesTupleMatchStatement(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "tuple_match_execute_fixture.llcontext")
	src := "@test\ndef tuple_match_selects_literal_arm() -> void:\n    match 5, 'w', 'h', 'i', 'l', 'e':\n        5, 'w', 'h', 'i', 'l', 'e':\n            return\n        _:\n            can Abort.Panic:\n                panic(\"tuple match fallback\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write tuple match execute fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected tuple match test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] tuple_match_selects_literal_arm",
		"[       OK ] tuple_match_selects_literal_arm",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tuple match execution output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesPanicToBacktraceAwareLLVM(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "panic_backtrace_fixture.llcontext")
	src := "def main() -> int:\n    can Abort.Panic:\n        panic(\"boom\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write panic backtrace fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected panic backtrace LLVM emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"declare i64 @printf(ptr, ...)",
		"declare i64 @backtrace(ptr, i64)",
		"declare void @backtrace_symbols_fd(ptr, i64, i64)",
		"declare void @abort()",
		"call i64 (ptr, ...) @printf(",
		"call i64 @backtrace(ptr",
		"call void @backtrace_symbols_fd(ptr",
		"call void @abort()",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected panic LLVM output to contain %q, got:\n%s", check, output)
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
