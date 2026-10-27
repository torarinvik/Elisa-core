package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalysisDiskArtifactWithPayload(t *testing.T) {
	t.Run("valid payload", func(t *testing.T) {
		artifact := analysisDiskArtifact{key: "payload-test", path: filepath.Join(t.TempDir(), "state")}
		want := []byte("encoded-state")
		if err := artifact.publish(want); err != nil {
			t.Fatal(err)
		}

		called := false
		err := artifact.withPayload(func(got []byte) error {
			called = true
			if !bytes.Equal(got, want) {
				t.Fatalf("payload = %q, want %q", got, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("payload callback was not called")
		}
	})

	t.Run("invalid header skips callback", func(t *testing.T) {
		artifact := analysisDiskArtifact{key: "expected-key", path: filepath.Join(t.TempDir(), "state")}
		data := append(artifact.header(), []byte("encoded-state")...)
		data[0] ^= 1
		if err := os.WriteFile(artifact.path, data, 0600); err != nil {
			t.Fatal(err)
		}

		called := false
		err := artifact.withPayload(func([]byte) error {
			called = true
			return nil
		})
		if err == nil {
			t.Fatal("withPayload accepted an invalid header")
		}
		if called {
			t.Fatal("payload callback ran for an invalid header")
		}
	})

	t.Run("callback error propagates", func(t *testing.T) {
		artifact := analysisDiskArtifact{key: "callback-error", path: filepath.Join(t.TempDir(), "state")}
		if err := artifact.publish([]byte("encoded-state")); err != nil {
			t.Fatal(err)
		}
		wantErr := errors.New("decode failed")
		err := artifact.withPayload(func([]byte) error { return wantErr })
		if !errors.Is(err, wantErr) {
			t.Fatalf("withPayload error = %v, want %v", err, wantErr)
		}
	})
}
