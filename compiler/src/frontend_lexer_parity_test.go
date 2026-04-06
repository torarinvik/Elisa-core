package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	golexer "llcontext/src/lexer"
)

const frontendLexerHashOffset uint64 = 1469598103934665603
const frontendLexerHashPrime uint64 = 1099511628211

func frontendLexerChecksumMix(hash uint64, value uint64) uint64 {
	return (hash ^ value) * frontendLexerHashPrime
}

func frontendLexerChecksumView(hash uint64, text string) uint64 {
	out := frontendLexerChecksumMix(hash, uint64(len(text)))
	for i := 0; i < len(text); i++ {
		out = frontendLexerChecksumMix(out, uint64(text[i]))
	}
	return out
}

func frontendLexerGoChecksum(sourcePath string, raw []byte) uint64 {
	l := golexer.New(sourcePath, raw)
	tokens := l.Tokenize()
	hash := frontendLexerChecksumMix(frontendLexerHashOffset, uint64(len(tokens)))
	for _, tok := range tokens {
		hash = frontendLexerChecksumMix(hash, uint64(tok.Kind)+1)
	}
	return hash
}

func buildFrontendLexerChecksumHarness(t *testing.T) string {
	t.Helper()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "frontend_lexer_runtime_shims.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "frontend_lexer.h")
	objectPath := filepath.Join(outputDir, "frontend_lexer.o")
	harnessPath := filepath.Join(outputDir, "frontend_lexer_checksum_harness.c")
	exePath := filepath.Join(outputDir, "frontend_lexer_checksum_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates exported lexer correctness and ABI wiring, not optimized code quality.
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

	harnessSource := `#include "frontend_lexer.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint8_t *read_file_cstr(const char *path) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        return NULL;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        return NULL;
    }
    long size = ftell(file);
    if (size < 0) {
        fclose(file);
        return NULL;
    }
    if (fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        return NULL;
    }
    uint8_t *buffer = (uint8_t *)malloc((size_t)size + 1u);
    if (buffer == NULL) {
        fclose(file);
        return NULL;
    }
    size_t read_count = fread(buffer, 1u, (size_t)size, file);
    fclose(file);
    if (read_count != (size_t)size) {
        free(buffer);
        return NULL;
    }
    buffer[size] = 0;
    return buffer;
}

int main(int argc, char **argv) {
	if (argc < 3) {
		fprintf(stderr, "usage: %s <count|checksum|kinds> <source-file>\n", argv[0]);
        return 2;
    }
	uint8_t *source = read_file_cstr(argv[2]);
    if (source == NULL) {
		fprintf(stderr, "failed to read %s\n", argv[2]);
        return 3;
    }
	if (strcmp(argv[1], "count") == 0) {
		printf("%lld\n", (long long)frontend_lexer_token_count(source));
	} else if (strcmp(argv[1], "checksum") == 0) {
		printf("%llu\n", (unsigned long long)frontend_lexer_token_checksum(source));
	} else if (strcmp(argv[1], "kinds") == 0) {
		uintptr_t count = (uintptr_t)frontend_lexer_token_count(source);
		uintptr_t alloc_count = count == 0 ? 1u : count;
		uint64_t *kinds = (uint64_t *)malloc(sizeof(uint64_t) * (size_t)alloc_count);
		if (kinds == NULL) {
			fprintf(stderr, "failed to allocate %llu token kinds\n", (unsigned long long)count);
			free(source);
			return 4;
		}
		uintptr_t written = frontend_lexer_copy_token_kinds(source, kinds, count);
		if (written != count) {
			fprintf(stderr, "frontend_lexer_copy_token_kinds returned %llu, expected %llu\n", (unsigned long long)written, (unsigned long long)count);
			free(kinds);
			free(source);
			return 5;
		}
		for (uintptr_t i = 0; i < count; i++) {
			printf("%llu\n", (unsigned long long)kinds[i]);
		}
		free(kinds);
	} else {
		fprintf(stderr, "unknown mode %s\n", argv[1]);
		free(source);
		return 6;
	}
    free(source);
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harnessSource), 0o644); err != nil {
		t.Fatalf("failed to write checksum harness: %v", err)
	}

	// Keep this harness on O0: optimized object generation is covered elsewhere.
	compileArgs := []string{"-O0", "-I", outputDir, harnessPath, shimPath, objectPath, "-o", exePath}
	if strings.Contains(strings.ToLower(runtime.GOOS), "darwin") {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	return exePath
}

func TestRunCLIFrontendLexerChecksumMatchesGoLexer(t *testing.T) {
	harnessExe := buildFrontendLexerChecksumHarness(t)
	repoRoot := repoRootFromMainTest(t)
	tmpDir := t.TempDir()

	writeCase := func(name string, content string) string {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
		return path
	}

	cases := []struct {
		name string
		path string
	}{
		{name: "simple-ident", path: writeCase("simple_ident.llcontext", "hello\n")},
		{name: "operators-and-indent", path: writeCase("ops_indent.llcontext", "def foo:\n    x <- 1\n    y <- 2\nz <- 3\n")},
		{name: "region-syntax", path: writeCase("region_syntax.llcontext", "region parse\nvalue: any i32& = new[parse] 1\ndestroy parse\n")},
		{name: "string-and-comment", path: writeCase("string_comment.llcontext", "\"hello\\nworld\" # comment\n")},
		{name: "char-literals", path: writeCase("char_literals.llcontext", "'a' '\\n' '\\x41' '\\u0041'\n")},
		{name: "all-keywords", path: writeCase("all_keywords.llcontext", "as if in or and any def not to try elif else enum heap null pass repr tail true with const error false match panic raise stack while export extern global packed return sizeof static struct zeroed aligned mutable context region new destroy\n")},
		{name: "self-hosted-source", path: filepath.Join(repoRoot, "Code", "frontend_llcontext", "frontend_lexer.llcontext")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("failed to read %s: %v", tc.path, err)
			}
			expected := frontendLexerGoChecksum(tc.path, raw)
			goTokens := golexer.New(tc.path, raw).Tokenize()

			countCmd := exec.Command(harnessExe, "count", tc.path)
			countOutput, err := countCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("count harness failed: %v\n%s", err, string(countOutput))
			}
			countText := strings.TrimSpace(string(countOutput))
			actualCount, err := strconv.Atoi(countText)
			if err != nil {
				t.Fatalf("failed to parse token count %q: %v", countText, err)
			}
			if actualCount != len(goTokens) {
				t.Fatalf("frontend lexer token count mismatch for %s: got %d want %d", tc.path, actualCount, len(goTokens))
			}

			kindsCmd := exec.Command(harnessExe, "kinds", tc.path)
			kindsOutput, err := kindsCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("kinds harness failed: %v\n%s", err, string(kindsOutput))
			}
			kindLines := strings.Fields(strings.TrimSpace(string(kindsOutput)))
			if len(kindLines) != len(goTokens) {
				t.Fatalf("frontend lexer token kind count mismatch for %s: got %d want %d", tc.path, len(kindLines), len(goTokens))
			}
			for i, tok := range goTokens {
				actualKind, err := strconv.ParseUint(kindLines[i], 10, 64)
				if err != nil {
					t.Fatalf("failed to parse kind %q at token %d: %v", kindLines[i], i, err)
				}
				expectedKind := uint64(tok.Kind) + 1
				if actualKind != expectedKind {
					t.Fatalf("frontend lexer token kind mismatch for %s at token %d: got %d want %d", tc.path, i, actualKind, expectedKind)
				}
			}

			cmd := exec.Command(harnessExe, "checksum", tc.path)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("checksum harness failed: %v\n%s", err, string(output))
			}
			actualText := strings.TrimSpace(string(output))
			actual, err := strconv.ParseUint(actualText, 10, 64)
			if err != nil {
				t.Fatalf("failed to parse checksum %q: %v", actualText, err)
			}

			if actual != expected {
				t.Fatalf("frontend lexer checksum mismatch for %s: got %d want %d", tc.path, actual, expected)
			}
		})
	}
}
