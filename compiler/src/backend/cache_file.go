package backend

import "os"

// Cache corruption is a miss. In particular, inspect the opened descriptor
// before reading any header: pipes/devices must never supply cache bytes.
func openRegularCacheFile(path string) (*os.File, error) {
	f, err := openCacheReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && info.Mode().IsRegular() {
		current, pathErr := os.Lstat(path)
		if pathErr == nil && current.Mode().IsRegular() && os.SameFile(info, current) {
			return f, nil
		}
	}
	_ = f.Close()
	return nil, os.ErrInvalid
}
