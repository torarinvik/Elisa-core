package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
)

type toolchainStampCacheEntry struct {
	fingerprint string
	stamp       string
	native      bool
}

// Cache content digests while the file identity and metadata stay unchanged. Hashing a
// multi-megabyte clang binary on every test-runner lookup is expensive, but caching by path
// forever can serve an object built by a replaced tool. Unix stat data includes device,
// inode and ctime, so atomic replacements and in-place writes invalidate the memo even when
// size and mtime are deliberately preserved. On platforms without that identity data we
// hash every time rather than trusting a weaker fingerprint.
var (
	toolchainStampMu    sync.Mutex
	toolchainStampCache = make(map[string]toolchainStampCacheEntry)
)

func toolchainContentStamp(path string) (string, error) {
	return toolchainContentStampWithNativeRequirement(path, false)
}

func toolchainNativeContentStamp(path string) (string, error) {
	return toolchainContentStampWithNativeRequirement(path, true)
}

func toolchainContentStampWithNativeRequirement(path string, requireNative bool) (string, error) {
	toolchainStampMu.Lock()
	defer toolchainStampMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		// Stat follows the selector in one filesystem lookup. Do not lexically
		// normalize paths: a symlink followed by /.. has filesystem semantics.
		before, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !before.Mode().IsRegular() {
			return "", fmt.Errorf("toolchain %q is not a regular file", path)
		}
		fingerprint, memoizable := toolchainFileFingerprint(before)
		if memoizable {
			if cached, ok := toolchainStampCache[path]; ok && cached.fingerprint == fingerprint {
				if !requireNative || cached.native {
					return cached.stamp, nil
				}
			}
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
			continue
		}
		if requireNative {
			var magic [4]byte
			if _, err := io.ReadFull(file, magic[:]); err != nil {
				file.Close()
				return "", err
			}
			if !nativeToolMagic(magic) {
				file.Close()
				return "", fmt.Errorf("toolchain %q is not a native executable image", path)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				file.Close()
				return "", err
			}
		}
		// Hash the descriptor whose identity was checked, not a newly opened path.
		stamp, hashErr := hashOpenToolchainFile(file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		if hashErr != nil {
			return "", hashErr
		}
		if statErr != nil {
			return "", statErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		selected, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !sameToolchainFileVersion(opened, after) || !sameToolchainFileVersion(after, selected) {
			continue
		}
		afterFingerprint, afterMemoizable := toolchainFileFingerprint(after)
		if afterMemoizable {
			toolchainStampCache[path] = toolchainStampCacheEntry{fingerprint: afterFingerprint, stamp: stamp, native: requireNative}
		} else {
			delete(toolchainStampCache, path)
		}
		return stamp, nil
	}
	return "", fmt.Errorf("toolchain %q changed repeatedly while hashing", path)
}

func nativeToolMagic(magic [4]byte) bool {
	switch string(magic[:]) {
	case "\x7fELF", "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe",
		"\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca", "\xca\xfe\xba\xbf", "\xbf\xba\xfe\xca":
		return true
	default:
		return false
	}
}

func sameToolchainFileVersion(a, b os.FileInfo) bool {
	if a == nil || b == nil || !a.Mode().IsRegular() || !b.Mode().IsRegular() || !os.SameFile(a, b) || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	first, firstStable := toolchainFileFingerprint(a)
	second, secondStable := toolchainFileFingerprint(b)
	return !firstStable || !secondStable || first == second
}

func hashOpenToolchainFile(file *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func toolchainFileFingerprint(info os.FileInfo) (string, bool) {
	if info == nil || (runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "freebsd") {
		return "", false
	}
	stat := reflect.ValueOf(info.Sys())
	if stat.Kind() == reflect.Pointer {
		stat = stat.Elem()
	}
	if !stat.IsValid() || stat.Kind() != reflect.Struct {
		return "", false
	}
	planValue, ok := toolchainFingerprintPlans.Load(stat.Type())
	if !ok {
		planValue, _ = toolchainFingerprintPlans.LoadOrStore(stat.Type(), makeToolchainFingerprintPlan(stat.Type()))
	}
	plan := planValue.(toolchainFingerprintPlan)
	if !plan.stable {
		return "", false
	}
	data := make([]byte, 0, 256)
	data = append(data, plan.typeName...)
	data = append(data, 0)
	data = binary.LittleEndian.AppendUint64(data, uint64(info.Mode()))
	data = binary.LittleEndian.AppendUint64(data, uint64(info.Size()))
	data = binary.LittleEndian.AppendUint64(data, uint64(info.ModTime().UnixNano()))
	for _, index := range plan.fields {
		var valid bool
		data, valid = appendToolchainFingerprintValue(data, stat.Field(index))
		if !valid {
			return "", false
		}
	}
	return string(data), true
}

type toolchainFingerprintPlan struct {
	fields   []int
	typeName string
	stable   bool
}

var toolchainFingerprintPlans sync.Map // reflect.Type -> immutable field plan

func makeToolchainFingerprintPlan(kind reflect.Type) toolchainFingerprintPlan {
	plan := toolchainFingerprintPlan{typeName: kind.PkgPath() + "/" + kind.String()}
	device, inode, change := false, false, false
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		lower := strings.ToLower(field.Name)
		// Match the prior full-stat identity: retain every accessible field
		// except access time, which may change when another process runs it.
		if field.PkgPath != "" || strings.Contains(lower, "atim") || strings.Contains(lower, "accesstime") {
			continue
		}
		plan.fields = append(plan.fields, index)
		device = device || field.Name == "Dev" || field.Name == "Device"
		inode = inode || field.Name == "Ino" || field.Name == "Inode"
		change = change || strings.Contains(lower, "ctim") || strings.Contains(lower, "changetime")
	}
	plan.stable = device && inode && change
	return plan
}

// Fixed-width scalar encoding avoids Interface boxing and per-field formatting.
// Unsupported metadata keeps the fresh-hash fallback rather than weakening it.
func appendToolchainFingerprintValue(data []byte, value reflect.Value) ([]byte, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return binary.LittleEndian.AppendUint64(data, uint64(value.Int())), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return binary.LittleEndian.AppendUint64(data, value.Uint()), true
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			var valid bool
			data, valid = appendToolchainFingerprintValue(data, value.Field(index))
			if !valid {
				return data, false
			}
		}
		return data, true
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			var valid bool
			data, valid = appendToolchainFingerprintValue(data, value.Index(index))
			if !valid {
				return data, false
			}
		}
		return data, true
	default:
		return data, false
	}
}
