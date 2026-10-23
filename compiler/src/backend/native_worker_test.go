//go:build cgo

package backend

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("ELISACORE_TEST_NATIVE_WORKERS") == "1" {
		RunPartitionWorkerIfRequested()
	}
	os.Exit(m.Run())
}
