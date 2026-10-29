package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheCertificateRefusesTransientSMTOutcome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "certificate")
	if writeCacheInvocationCertificate([]string{"-emit", "semantic"}, path, false) {
		t.Fatal("transient SMT outcome received a cache certificate")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("uncertifiable invocation wrote a certificate: %v", err)
	}
}

func TestCacheCertificateEmitMode(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"-emit", "obj", "input.elisa"}, want: emitObject},
		{args: []string{"-emit=object", "input.elisa"}, want: emitObject},
		{args: []string{"input.elisa", "-emit=semantic"}, want: emitSemantic},
		{args: []string{"input.elisa"}, want: ""},
	} {
		if got := cacheEmitMode(test.args); got != test.want {
			t.Errorf("cacheEmitMode(%q) = %q, want %q", test.args, got, test.want)
		}
	}
}
