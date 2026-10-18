package backend

import (
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
