package main

import (
	"context"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const testRunnerLinkerProbeTimeout = 5 * time.Second

// testRunnerLinkerQueryArgs asks the same Clang driver to resolve ld using the
// target and linker-selection flags that will be present on the actual link.
// The driver's selected linker is an input to both persistent test-runner caches.
func testRunnerLinkerQueryArgs(targetTriple string, linkFlags []string) []string {
	args := append([]string{}, targetClangArgs(targetTriple)...)
	args = append(args, targetLinkerArgs(targetTriple)...)
	args = append(args, linkFlags...)
	return append(args, "-print-prog-name=ld")
}

func resolveTestRunnerLinkerPath(driverOutput string, targetTriple string) (string, error) {
	selected := strings.TrimSpace(driverOutput)
	if selected == "" || strings.ContainsAny(selected, "\r\n") {
		return "", fmt.Errorf("clang returned an invalid linker selector %q", driverOutput)
	}
	if !filepath.IsAbs(selected) {
		// Apple Clang may use xcrun/toolchain selection for a linker whose driver
		// spelling is only "ld". PATH lookup alone cannot prove that selection.
		if isMachOTriple(targetTriple) {
			return "", fmt.Errorf("clang returned an ambiguous linker selector %q for a Mach-O target", selected)
		}
		resolved, err := exec.LookPath(selected)
		if err != nil {
			return "", fmt.Errorf("resolve clang linker %q: %w", selected, err)
		}
		selected = resolved
	}
	if !filepath.IsAbs(selected) {
		resolved, err := filepath.Abs(selected)
		if err != nil {
			return "", err
		}
		selected = resolved
	}
	info, err := os.Stat(selected)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("clang linker %q is not an executable regular file", selected)
	}
	return selected, nil
}

func writeTestRunnerLinkerCacheIdentity(hash hash.Hash, linkerPath string) error {
	info, err := os.Stat(linkerPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("clang linker %q is not an executable regular file", linkerPath)
	}
	stamp, err := toolchainNativeContentStamp(linkerPath)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "linker="+linkerPath)
	testRunnerCacheWriteString(hash, "linker-content="+stamp)
	return nil
}

func writeTestRunnerLinkerCacheInputs(hash hash.Hash, clangPath string, targetTriple string, linkFlags []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), testRunnerLinkerProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, clangPath, testRunnerLinkerQueryArgs(targetTriple, linkFlags)...)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("query clang linker selection: %w", ctx.Err())
		}
		return fmt.Errorf("query clang linker selection: %w", err)
	}
	linkerPath, err := resolveTestRunnerLinkerPath(string(output), targetTriple)
	if err != nil {
		return err
	}
	if err := writeTestRunnerLinkerCacheIdentity(hash, linkerPath); err != nil {
		return err
	}
	// /usr/bin/ld on macOS delegates to the active developer toolchain. Hash
	// xcrun's selected linker too, so an Xcode/toolchain switch at the same
	// system selector cannot leave an old test executable eligible for reuse.
	if runtime.GOOS == "darwin" && filepath.Clean(linkerPath) == "/usr/bin/ld" {
		// Use the system dispatcher that /usr/bin/ld is paired with; PATH may
		// contain an unrelated xcrun earlier in the search order.
		const xcrunPath = "/usr/bin/xcrun"
		if err := writeTestRunnerLinkerCacheIdentity(hash, xcrunPath); err != nil {
			return err
		}
		xcrunCtx, xcrunCancel := context.WithTimeout(context.Background(), testRunnerLinkerProbeTimeout)
		defer xcrunCancel()
		xcrun := exec.CommandContext(xcrunCtx, xcrunPath, "--find", "ld")
		resolvedOutput, err := xcrun.Output()
		if err != nil {
			if xcrunCtx.Err() != nil {
				return fmt.Errorf("resolve active macOS linker with xcrun: %w", xcrunCtx.Err())
			}
			return fmt.Errorf("resolve active macOS linker with xcrun: %w", err)
		}
		resolvedPath, err := resolveTestRunnerLinkerPath(string(resolvedOutput), targetTriple)
		if err != nil {
			return fmt.Errorf("resolve active macOS linker with xcrun: %w", err)
		}
		if filepath.Clean(resolvedPath) == "/usr/bin/ld" {
			return fmt.Errorf("xcrun did not resolve /usr/bin/ld to a developer-toolchain linker")
		}
		if err := writeTestRunnerLinkerCacheIdentity(hash, resolvedPath); err != nil {
			return err
		}
	}
	return nil
}

func testRunnerLinkInputContentStamp(path string) (string, error) {
	firstStamp, err := toolchainContentStamp(path)
	if err != nil {
		return "", err
	}
	before, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("link input %q is not a regular file", path)
	}
	file, err := openToolchainStampFile(path)
	if err != nil {
		return "", err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return "", err
	}
	if !sameToolchainFileVersion(before, opened) {
		file.Close()
		return "", fmt.Errorf("link input %q changed while inspecting it", path)
	}
	var header [8]byte
	count, readErr := io.ReadFull(file, header[:])
	after, statErr := file.Stat()
	selected, selectedErr := os.Stat(path)
	closeErr := file.Close()
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", readErr
	}
	if statErr != nil {
		return "", statErr
	}
	if selectedErr != nil {
		return "", selectedErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if !sameToolchainFileVersion(opened, after) || !sameToolchainFileVersion(after, selected) {
		return "", fmt.Errorf("link input %q changed while inspecting it", path)
	}
	if count == len(header) && string(header[:]) == "!<thin>\n" {
		return "", fmt.Errorf("thin archive %q has external members", path)
	}
	var magic [4]byte
	copy(magic[:], header[:4])
	objectImage := count >= 4 && (nativeToolMagic(magic) || string(header[:4]) == "BC\xc0\xde" || string(header[:4]) == "\xde\xc0\x17\x0b")
	archive := count == len(header) && string(header[:]) == "!<arch>\n"
	if !archive && !objectImage {
		return "", fmt.Errorf("link input %q is not a self-contained object or archive", path)
	}
	secondStamp, err := toolchainContentStamp(path)
	if err != nil {
		return "", err
	}
	if firstStamp != secondStamp {
		return "", fmt.Errorf("link input %q changed while hashing it", path)
	}
	return secondStamp, nil
}

// writeTestRunnerLinkDependencyInputs keys explicit self-contained object and
// archive files. Library searches, response files, linker scripts, plugins, and
// unclassified flags can select additional files that this cache cannot resolve,
// so those requests build cold and are not published.
func writeTestRunnerLinkDependencyInputs(hash hash.Hash, linkFlags []string) error {
	for index := 0; index < len(linkFlags); index++ {
		flag := strings.TrimSpace(linkFlags[index])
		if flag == "" {
			continue
		}
		switch {
		case strings.HasPrefix(flag, "-fuse-ld=") && strings.TrimSpace(strings.TrimPrefix(flag, "-fuse-ld=")) != "":
			// The selected linker path and bytes are queried and hashed separately.
		default:
			if strings.HasPrefix(flag, "-") || strings.HasPrefix(flag, "@") {
				return fmt.Errorf("test-runner cache cannot key opaque link input %q", flag)
			}
			path, err := filepath.Abs(flag)
			if err != nil {
				return err
			}
			stamp, err := testRunnerLinkInputContentStamp(path)
			if err != nil {
				return err
			}
			testRunnerCacheWriteString(hash, "link-input="+path)
			testRunnerCacheWriteString(hash, "link-input-content="+stamp)
		}
	}
	return nil
}

func writeTestRunnerForeignLinkInputs(hash hash.Hash, foreignFiles []string) error {
	for _, foreignFile := range foreignFiles {
		trimmed := strings.TrimSpace(foreignFile)
		if trimmed == "" {
			continue
		}
		switch strings.ToLower(filepath.Ext(trimmed)) {
		case ".c", ".cc", ".cpp", ".cxx", ".c++", ".m", ".mm", ".s", ".asm":
			// The text include expander below is not a C/C++ preprocessor and
			// cannot represent angle-bracket, macro, or conditional includes.
			return fmt.Errorf("test-runner cache cannot key preprocessed foreign source %q", trimmed)
		}
		path, err := filepath.Abs(trimmed)
		if err != nil {
			return err
		}
		stamp, err := testRunnerLinkInputContentStamp(path)
		if err != nil {
			return err
		}
		testRunnerCacheWriteString(hash, "foreign-link-input="+path)
		testRunnerCacheWriteString(hash, "foreign-link-content="+stamp)
	}
	return nil
}
