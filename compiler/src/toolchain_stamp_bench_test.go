package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func BenchmarkToolchainContentStampWarm(b *testing.B) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	tool := filepath.Join(dir, "tool")
	if err := os.WriteFile(tool, []byte("stable tool contents"), 0600); err != nil {
		b.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(tool, link); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct{ name, path string }{{"clang", clang}, {"symlink", link}} {
		b.Run(tc.name, func(b *testing.B) {
			info, err := os.Stat(tc.path)
			if err != nil {
				b.Fatal(err)
			}
			if _, memoizable := toolchainFileFingerprint(info); !memoizable {
				b.Skip("requires stable file identity metadata")
			}
			want, err := toolchainContentStamp(tc.path)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := toolchainContentStamp(tc.path)
				if err != nil || got != want {
					b.Fatal("tool identity changed", err)
				}
			}
		})
	}
}
