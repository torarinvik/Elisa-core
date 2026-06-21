//go:build cgo

package semantic

import (
	"io"
	"os/exec"
)

func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	cmd := exec.Command("go", append([]string{"run", "../"}, args...)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}
