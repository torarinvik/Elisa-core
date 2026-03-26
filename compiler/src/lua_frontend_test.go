package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIExecutesLlcontextLuaFrontendTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "llcontext_lua", "test", "lua_frontend_tests.llcontext")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to execute llcontext_lua tests successfully, stderr:\n%s", stderr.String())
	}
	if stderrOutput := strings.TrimSpace(stderr.String()); stderrOutput != "" {
		for _, line := range strings.Split(stderrOutput, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.Contains(trimmed, "warning:") {
				t.Fatalf("expected only warning output on stderr, got:\n%s", stderr.String())
			}
		}
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] sample_frontend_checksum_is_stable",
		"[       OK ] sample_frontend_checksum_is_stable",
		"[ RUN      ] binary_operator_weights_are_stable",
		"[       OK ] binary_operator_weights_are_stable",
		"[ RUN      ] token_and_cursor_helpers_are_stable",
		"[       OK ] token_and_cursor_helpers_are_stable",
		"[ RUN      ] keyword_lookup_recognizes_all_keywords",
		"[       OK ] keyword_lookup_recognizes_all_keywords",
		"[ RUN      ] keyword_lookup_is_case_sensitive_and_prefix_safe",
		"[       OK ] keyword_lookup_is_case_sensitive_and_prefix_safe",
		"[ RUN      ] lexer_reads_keywords_names_numbers_and_punct",
		"[       OK ] lexer_reads_keywords_names_numbers_and_punct",
		"[ RUN      ] lexer_skips_comments_tracks_lines_and_reads_compound_tokens",
		"[       OK ] lexer_skips_comments_tracks_lines_and_reads_compound_tokens",
		"[ RUN      ] lexer_reads_all_keywords_in_sequence",
		"[       OK ] lexer_reads_all_keywords_in_sequence",
		"[ RUN      ] lexer_treats_keyword_prefixes_and_case_variants_as_names",
		"[       OK ] lexer_treats_keyword_prefixes_and_case_variants_as_names",
		"[ RUN      ] lexer_tracks_keyword_spans_next_to_punctuation",
		"[       OK ] lexer_tracks_keyword_spans_next_to_punctuation",
		"[ RUN      ] string_view_equality_syntax_is_stable",
		"[       OK ] string_view_equality_syntax_is_stable",
		"[ RUN      ] parser_builds_two_statement_chunk_with_precedence",
		"[       OK ] parser_builds_two_statement_chunk_with_precedence",
		"[ RUN      ] parser_handles_unary_and_grouped_expressions",
		"[       OK ] parser_handles_unary_and_grouped_expressions",
		"[ SUMMARY  ] 13 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llcontext_lua test output to contain %q, got:\n%s", check, output)
		}
	}
}
