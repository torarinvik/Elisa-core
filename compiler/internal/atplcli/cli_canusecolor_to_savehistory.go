package atplcli

import (
	"github.com/peterh/liner"
	"io"
	"os"
	"path/filepath"
)

func canUseColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if term := os.Getenv("TERM"); term == "" || term == "dumb" {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return false
	}
	return IsInteractive(outFile)
}
func IsInteractive(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
func replHistoryFile() string {
	configDir, err := os.UserConfigDir()
	if err == nil {
		dir := filepath.Join(configDir, "atpl")
		if mkdirErr := os.MkdirAll(dir, 0o755); mkdirErr == nil {
			return filepath.Join(dir, "history")
		}
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(homeDir, ".atpl_history")
	}

	return ""
}
func loadHistory(line *liner.State, path string) {
	if path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = line.ReadHistory(file)
}
func saveHistory(line *liner.State, path string) {
	if path == "" {
		return
	}
	file, err := os.Create(path)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = line.WriteHistory(file)
}
