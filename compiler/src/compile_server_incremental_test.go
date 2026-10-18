package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func postCompileServerRequest(t *testing.T, handler http.Handler, request compileServerRequest) (compileServerResponse, int) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/compile", bytes.NewReader(body))
	handler.ServeHTTP(recorder, httpRequest)
	var response compileServerResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode compile server response: %v\n%s", err, recorder.Body.String())
	}
	return response, recorder.Code
}

func compileServerResponsesEqual(left, right compileServerResponse) bool {
	return left.OK == right.OK && left.Output == right.Output && left.IR == right.IR && left.Object == right.Object && left.Value == right.Value && left.Stderr == right.Stderr && left.Error == right.Error && left.ErrorCode == right.ErrorCode
}

func TestCompileServerReusesBodiesAcrossRequestsAndInvalidatesEdits(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(8)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	filename := filepath.Join(t.TempDir(), "server_incremental.elisa")
	source := "def value() -> i64:\n    return 20 + 1\n"
	request := compileServerRequest{Mode: "semantic", Filename: filename, Source: source, Incremental: true}

	cold, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !cold.OK {
		t.Fatalf("cold compile failed: status=%d response=%+v", status, cold)
	}
	session := cache.session(filename, emitSemantic)
	first := session.Stats()
	if first.PrimaryBodiesChecked != 1 || first.PrimaryBodiesReused != 0 {
		t.Fatalf("first request did not perform cold primary typing: %+v", first)
	}

	warm, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !warm.OK || warm.Output != cold.Output || warm.Stderr != cold.Stderr {
		t.Fatalf("warm compile changed its result: status=%d cold=%+v warm=%+v", status, cold, warm)
	}
	second := session.Stats()
	if second.PrimaryBodiesReused-first.PrimaryBodiesReused != 1 || second.PrimaryBodiesChecked != first.PrimaryBodiesChecked {
		t.Fatalf("server request did not reuse semantic typing: first=%+v second=%+v", first, second)
	}

	request.Source = strings.Replace(source, "20 + 1", "20 + 2", 1)
	edited, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !edited.OK || edited.Output == warm.Output {
		t.Fatalf("edited compile did not produce a fresh result: status=%d response=%+v", status, edited)
	}
	third := session.Stats()
	if third.PrimaryBodiesChecked-second.PrimaryBodiesChecked != 1 || third.PrimaryBodiesReused != second.PrimaryBodiesReused {
		t.Fatalf("body edit did not invalidate only its cached typing: second=%+v third=%+v", second, third)
	}
}

func TestCompileServerIncrementalReuseRequiresOptIn(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	request := compileServerRequest{
		Mode:     "semantic",
		Filename: filepath.Join(t.TempDir(), "opt_in.elisa"),
		Source:   "def value() -> i64:\n    return 20 + 1\n",
	}

	stateless, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !stateless.OK {
		t.Fatalf("stateless compile failed: status=%d response=%+v", status, stateless)
	}
	cache.mu.Lock()
	entries := len(cache.entries)
	cache.mu.Unlock()
	if entries != 0 {
		t.Fatalf("request without incremental opt-in created %d cached sessions", entries)
	}

	request.Incremental = true
	if response, status := postCompileServerRequest(t, handler, request); status != http.StatusOK || !response.OK {
		t.Fatalf("incremental compile failed: status=%d response=%+v", status, response)
	}
	cache.mu.Lock()
	entries = len(cache.entries)
	cache.mu.Unlock()
	if entries != 1 {
		t.Fatalf("incremental opt-in created %d cached sessions, want 1", entries)
	}

	formatRequest := request
	formatRequest.Mode = "fmt"
	if response, status := postCompileServerRequest(t, handler, formatRequest); status != http.StatusOK || !response.OK {
		t.Fatalf("format request failed: status=%d response=%+v", status, response)
	}
	cache.mu.Lock()
	entries = len(cache.entries)
	cache.mu.Unlock()
	if entries != 1 {
		t.Fatalf("non-analyzing format request created a cached session; entries=%d", entries)
	}
}

func TestCompileServerInspectionModesReuseWithoutChangingOutput(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	request := compileServerRequest{
		Mode:        "ast",
		Filename:    filepath.Join(t.TempDir(), "inspection.elisa"),
		Source:      "def value() -> i64:\n    return 20 + 1\n",
		Incremental: true,
	}
	cold, status := executeCompileServerRequest(request)
	if status != http.StatusOK || !cold.OK {
		t.Fatalf("cold AST inspection failed: status=%d response=%+v", status, cold)
	}
	warm, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !warm.OK || warm.Output != cold.Output || warm.Stderr != cold.Stderr {
		t.Fatalf("incremental AST inspection changed output: status=%d cold=%+v warm=%+v", status, cold, warm)
	}
	session := cache.session(request.Filename, emitAST)
	first := session.Stats()
	if first.PrimaryBodiesChecked != 1 || first.PrimaryBodiesReused != 0 {
		t.Fatalf("first inspection did not run cold semantic checks: %+v", first)
	}
	if response, status := postCompileServerRequest(t, handler, request); status != http.StatusOK || !response.OK {
		t.Fatalf("warm AST inspection failed: status=%d response=%+v", status, response)
	}
	second := session.Stats()
	if second.PrimaryBodiesReused-first.PrimaryBodiesReused != 1 || second.PrimaryBodiesChecked != first.PrimaryBodiesChecked {
		t.Fatalf("inspection warning analysis did not reuse typing: first=%+v second=%+v", first, second)
	}
}

func TestCompileServerBackendOutputParityAndReuse(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	directory := t.TempDir()
	for _, mode := range []string{"ir", "llvm"} {
		t.Run(mode, func(t *testing.T) {
			request := compileServerRequest{
				Mode:     mode,
				Filename: filepath.Join(directory, mode+"_parity.elisa"),
				Source:   "def value() -> i64:\n    return 20 + 1\n",
			}
			coldReference, status := executeCompileServerRequest(request)
			if status != http.StatusOK || !coldReference.OK {
				t.Fatalf("cold reference compile failed: status=%d response=%+v", status, coldReference)
			}

			request.Incremental = true
			coldIncremental, status := postCompileServerRequest(t, handler, request)
			if status != http.StatusOK || !coldIncremental.OK || !compileServerResponsesEqual(coldIncremental, coldReference) {
				t.Fatalf("cold incremental output differs from stateless output: status=%d reference=%+v incremental=%+v", status, coldReference, coldIncremental)
			}
			session := cache.session(request.Filename, emitLLVM)
			first := session.Stats()
			if first.PrimaryBodiesChecked != 1 || first.PrimaryBodiesReused != 0 {
				t.Fatalf("first %s request did not type its body cold: %+v", mode, first)
			}

			warm, status := postCompileServerRequest(t, handler, request)
			if status != http.StatusOK || !warm.OK || !compileServerResponsesEqual(warm, coldReference) {
				t.Fatalf("warm incremental output differs from stateless output: status=%d reference=%+v warm=%+v", status, coldReference, warm)
			}
			second := session.Stats()
			if second.PrimaryBodiesReused-first.PrimaryBodiesReused != 1 || second.PrimaryBodiesChecked != first.PrimaryBodiesChecked {
				t.Fatalf("warm %s request did not reuse semantic typing: first=%+v second=%+v", mode, first, second)
			}
		})
	}
}

func TestCompileServerConcurrentRequestsShareSessionSafely(t *testing.T) {
	const requestCount = 8
	cache := newCompileServerArtifactOnlyTestCache(2)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	request := compileServerRequest{
		Mode:        "semantic",
		Filename:    filepath.Join(t.TempDir(), "concurrent.elisa"),
		Source:      "def value() -> i64:\n    return 20 + 1\n",
		Incremental: true,
	}
	type outcome struct {
		response compileServerResponse
		status   int
	}
	outcomes := make(chan outcome, requestCount)
	var workers sync.WaitGroup
	workers.Add(requestCount)
	for range requestCount {
		go func() {
			defer workers.Done()
			response, status := postCompileServerRequest(t, handler, request)
			outcomes <- outcome{response: response, status: status}
		}()
	}
	workers.Wait()
	close(outcomes)
	for result := range outcomes {
		if result.status != http.StatusOK || !result.response.OK {
			t.Fatalf("concurrent compile failed: status=%d response=%+v", result.status, result.response)
		}
	}
	session := cache.session(request.Filename, emitSemantic)
	stats := session.Stats()
	if stats.PrimaryBodiesChecked != 1 || stats.PrimaryBodiesReused != requestCount-1 {
		t.Fatalf("concurrent requests did not serialize safely through one session: %+v", stats)
	}
}

func TestCompileServerSessionsSeparateFilenamesAndAnalysisProfiles(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(8)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	filename := filepath.Join(t.TempDir(), "profile.elisa")
	request := compileServerRequest{
		Mode:        "semantic",
		Filename:    filename,
		Source:      "def value() -> i64:\n    return 20 + 1\n",
		Incremental: true,
	}
	if response, status := postCompileServerRequest(t, handler, request); status != http.StatusOK || !response.OK {
		t.Fatalf("default profile failed: status=%d response=%+v", status, response)
	}
	defaultSession := cache.session(filename, emitSemantic)
	if defaultSession.Stats().PrimaryBodiesReused != 0 {
		t.Fatal("new filename session unexpectedly reused another file's product")
	}
	secondFilename := filepath.Join(t.TempDir(), "other_profile.elisa")
	secondRequest := request
	secondRequest.Filename = secondFilename
	if response, status := postCompileServerRequest(t, handler, secondRequest); status != http.StatusOK || !response.OK {
		t.Fatalf("second filename failed: status=%d response=%+v", status, response)
	}
	secondSession := cache.session(secondFilename, emitSemantic)
	if secondSession == defaultSession || secondSession.Stats().PrimaryBodiesReused != 0 {
		t.Fatal("different filenames shared semantic products")
	}
	if response, status := postCompileServerRequest(t, handler, secondRequest); status != http.StatusOK || !response.OK {
		t.Fatalf("warm second filename failed: status=%d response=%+v", status, response)
	}
	if secondSession.Stats().PrimaryBodiesReused != 1 {
		t.Fatalf("second filename failed to reuse its own product: %+v", secondSession.Stats())
	}

	request.Mode = "unsafe"
	if response, status := postCompileServerRequest(t, handler, request); status != http.StatusOK || !response.OK {
		t.Fatalf("unsafe profile failed: status=%d response=%+v", status, response)
	}
	unsafeSession := cache.session(filename, emitUnsafe)
	if unsafeSession == defaultSession || unsafeSession.Stats().PrimaryBodiesReused != 0 {
		t.Fatal("analysis profiles shared a semantic session")
	}
	if response, status := postCompileServerRequest(t, handler, request); status != http.StatusOK || !response.OK {
		t.Fatalf("warm unsafe profile failed: status=%d response=%+v", status, response)
	}
	if unsafeSession.Stats().PrimaryBodiesReused != 1 {
		t.Fatalf("unsafe profile did not reuse its own session: %+v", unsafeSession.Stats())
	}
}

func TestCompileServerSessionCacheEvictsLeastRecentlyUsedEntries(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(2)
	first := cache.session("first.elisa", emitSemantic)
	second := cache.session("second.elisa", emitSemantic)
	if cache.session("first.elisa", emitSemantic) != first {
		t.Fatal("cache replaced a recently used session")
	}
	cache.session("third.elisa", emitSemantic)
	if cache.session("first.elisa", emitSemantic) != first {
		t.Fatal("cache evicted the most recently used session")
	}
	if cache.session("second.elisa", emitSemantic) == second {
		t.Fatal("cache retained the least recently used session")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 2 {
		t.Fatalf("cache exceeded its entry bound: %d", len(cache.entries))
	}
}
