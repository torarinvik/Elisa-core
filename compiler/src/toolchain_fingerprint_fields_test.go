package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type toolchainFingerprintInfo struct {
	os.FileInfo
	sys any
}

func (i toolchainFingerprintInfo) Sys() any { return i.sys }

func visitToolchainStatValues(value reflect.Value, visit func(reflect.Value)) {
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			visitToolchainStatValues(value.Field(i), visit)
		}
	case reflect.Array:
		for i := 0; i < value.Len(); i++ {
			visitToolchainStatValues(value.Index(i), visit)
		}
	default:
		visit(value)
	}
}

func TestToolchainFingerprintPreservesFullStatIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		t.Skip("Unix strong stat identity")
	}
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("tool"), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original := reflect.ValueOf(info.Sys())
	if original.Kind() == reflect.Pointer {
		original = original.Elem()
	}
	baseline, stable := toolchainFileFingerprint(info)
	if !stable {
		t.Fatal("real Unix stat was not memoizable")
	}
	checked := 0
	for index := 0; index < original.NumField(); index++ {
		field := original.Type().Field(index)
		if field.PkgPath != "" {
			continue
		}
		clone := reflect.New(original.Type())
		clone.Elem().Set(original)
		visitToolchainStatValues(clone.Elem().Field(index), func(value reflect.Value) {
			old := reflect.New(value.Type()).Elem()
			old.Set(value)
			switch value.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				value.SetInt(value.Int() + 1)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				value.SetUint(value.Uint() + 1)
			default:
				t.Fatalf("uncovered stat field %s (%s)", field.Name, value.Kind())
			}
			changed, stable := toolchainFileFingerprint(toolchainFingerprintInfo{info, clone.Interface()})
			value.Set(old)
			if !stable {
				t.Fatalf("stat field %s disabled identity", field.Name)
			}
			name := strings.ToLower(field.Name)
			access := strings.Contains(name, "atim") || strings.Contains(name, "accesstime")
			if (changed == baseline) != access {
				t.Fatalf("field %s: equal=%v access=%v", field.Name, changed == baseline, access)
			}
			checked++
		})
	}
	if checked == 0 {
		t.Fatal("no stat fields checked")
	}
	malformed := struct {
		Dev, Ino uint64
		Ctime    float64
	}{1, 2, 3}
	if _, stable := toolchainFileFingerprint(toolchainFingerprintInfo{info, malformed}); stable {
		t.Fatal("unsupported metadata bypassed fresh-hash fallback")
	}
}
