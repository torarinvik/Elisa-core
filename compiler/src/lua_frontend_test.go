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
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] sample_frontend_checksum_is_stable",
		"[       OK ] sample_frontend_checksum_is_stable",
		"[ RUN      ] binary_operator_weights_are_stable",
		"[       OK ] binary_operator_weights_are_stable",
		"[ RUN      ] token_and_cursor_helpers_are_stable",
		"[       OK ] token_and_cursor_helpers_are_stable",
		"[ RUN      ] lexer_reads_keywords_names_numbers_and_punct",
		"[       OK ] lexer_reads_keywords_names_numbers_and_punct",
		"[ RUN      ] lexer_skips_comments_tracks_lines_and_reads_compound_tokens",
		"[       OK ] lexer_skips_comments_tracks_lines_and_reads_compound_tokens",
		"[ RUN      ] string_view_equality_syntax_is_stable",
		"[       OK ] string_view_equality_syntax_is_stable",
		"[ RUN      ] parser_builds_two_statement_chunk_with_precedence",
		"[       OK ] parser_builds_two_statement_chunk_with_precedence",
		"[ RUN      ] parser_handles_unary_and_grouped_expressions",
		"[       OK ] parser_handles_unary_and_grouped_expressions",
		"[ SUMMARY  ] 8 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llcontext_lua test output to contain %q, got:\n%s", check, output)
		}
	}
}
