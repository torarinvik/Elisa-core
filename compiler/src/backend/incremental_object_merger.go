package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Explicit merger commands remain request-local. The default toolchain is pinned
// on first use for this object's session, like the retained native artifacts.
func (s *IncrementalObjectSession) objectMergerCommand() []string {
	if fields := strings.Fields(strings.TrimSpace(os.Getenv("ELISA_LD"))); len(fields) != 0 {
		return fields
	}
	s.mergerOnce.Do(func() {
		s.defaultMerger = "ld"
		if runtime.GOOS != "darwin" {
			return
		}
		// Respect PATH-provided custom linkers. Only bypass Apple's dispatch shim.
		selected, err := exec.LookPath("ld")
		if err != nil || selected != "/usr/bin/ld" {
			return
		}
		output, err := exec.Command("/usr/bin/xcrun", "--find", "ld").Output()
		if err != nil {
			return
		}
		path := strings.TrimSpace(string(output))
		if !filepath.IsAbs(path) {
			return
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			s.defaultMerger = path
		}
	})
	return []string{s.defaultMerger}
}

// ObjectMergerCacheInputs describes the actual command and executable used by
// this session. The executable path is canonical for content stamping; command
// keeps the original argv (including argv[0], which can affect linker behavior).
// These are identity inputs, not replacement execution arguments.
func (s *IncrementalObjectSession) ObjectMergerCacheInputs() ([]string, string, error) {
	if s == nil {
		return nil, "", fmt.Errorf("no native object session")
	}
	command := s.objectMergerCommand()
	if len(command) == 0 {
		return nil, "", fmt.Errorf("no object merger")
	}
	selected, err := exec.LookPath(command[0])
	if err != nil {
		return nil, "", err
	}
	selected, err = filepath.Abs(selected)
	if err != nil {
		return nil, "", err
	}
	selected, err = filepath.EvalSymlinks(selected)
	if err != nil {
		return nil, "", err
	}
	return append([]string(nil), command...), selected, nil
}
