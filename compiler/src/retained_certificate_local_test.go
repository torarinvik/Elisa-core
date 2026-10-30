package main

import "testing"

func TestRetainedCertificateRequiresLocalExecution(t *testing.T) {
	clearAnalysisDiskObserverEnvironment(t)
	t.Setenv("ELISA_BACKEND_PARITY_OUT", "")
	t.Setenv("ELISACORE_CACHE_CERTIFICATE", "")
	if retainedRequestRequiresLocalProcess() {
		t.Fatal("ordinary request unexpectedly requires local execution")
	}
	t.Setenv("ELISACORE_CACHE_CERTIFICATE", "certificate.json")
	if !retainedRequestRequiresLocalProcess() {
		t.Fatal("certificate-backed request can be forwarded to a retained server")
	}
}
