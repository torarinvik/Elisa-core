package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// TestArenaWindowsBackendTypeChecks guards the Windows VirtualAlloc arena backend in
// runtime/elisacore_std/arena.elisa, which is gated behind
// `static if ARENA_BACKEND == ARENA_BACKEND_WIN32_VIRTUALALLOC` and is therefore
// COMPILED OUT on the POSIX hosts used for development. Without this test the Windows
// branch is never type-checked anywhere in CI and can silently rot (undefined idents,
// extern-signature mismatches, bad casts) until someone actually builds for Windows.
//
// Selecting a Windows target triple makes ELISA_TARGET_OS_WINDOWS true, which selects
// ARENA_BACKEND_WIN32_VIRTUALALLOC, which compiles in new_region's VirtualAllocEx path,
// new_region_reserve, arena_region_ensure_committed, free_region's VirtualFreeEx path,
// and the WIN_COMMIT_CHUNK / win_commit_round_up helpers. We assert analysis produces
// no error-level diagnostics for that branch.
//
// Manual equivalent:
//   elisac -emit semantic -target-triple=x86_64-pc-windows-msvc \
//       runtime/elisacore_std/arena.elisa
// (exit 0 == clean).
func TestArenaWindowsBackendTypeChecks(t *testing.T) {
	// Tests run with CWD = the package dir (src/semantic); the runtime tree is two
	// levels up under compiler/runtime.
	arenaPath := filepath.Join("..", "..", "runtime", "elisacore_std", "arena.elisa")
	src, err := os.ReadFile(arenaPath)
	if err != nil {
		t.Fatalf("read arena.elisa: %v", err)
	}

	l := lexer.New(arenaPath, src)
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors in arena.elisa: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(arenaPath)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors in arena.elisa: %v", errs)
	}

	// x86_64-pc-windows-msvc -> ELISA_TARGET_OS_WINDOWS -> ARENA_BACKEND_WIN32_VIRTUALALLOC.
	result := AnalyzeWithOptions(file, AnalyzeOptions{TargetTriple: "x86_64-pc-windows-msvc"})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("arena.elisa Windows (VirtualAlloc) backend has type errors:\n%s",
			strings.Join(errs, "\n"))
	}
}
