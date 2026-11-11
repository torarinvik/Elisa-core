package main

import (
	"os"
	"path/filepath"
)

// Each expansion resolves shared parent directories once. Keep this cache local
// to the expansion so later builds observe symlink changes in the include tree.
type sourceLabelResolver map[string]string

func (r sourceLabelResolver) resolve(filename string) string {
	// Check every leaf: a file symlink may point outside its parent directory.
	info, err := os.Lstat(filename)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		if canonical, err := filepath.EvalSymlinks(filename); err == nil {
			return filepath.Clean(canonical)
		}
		return filename
	}
	parent := filepath.Dir(filename)
	canonical, ok := r[parent]
	if !ok {
		canonical, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return filename
		}
		r[parent] = canonical
	}
	return filepath.Join(canonical, filepath.Base(filename))
}
