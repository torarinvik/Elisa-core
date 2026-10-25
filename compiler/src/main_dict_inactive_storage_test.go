package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runActualDictInactiveNative(t *testing.T, body string, expectOverflow bool) {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	root := repoRootFromMainTest(t)
	include := filepath.Join(root, "compiler/runtime/elisacore_std/heap.elisa")
	directory := t.TempDir()
	input := filepath.Join(directory, "inactive.elisa")
	object := filepath.Join(directory, "inactive.o")
	binary := filepath.Join(directory, "inactive")
	runtimeInclude := filepath.Join(root, "compiler/runtime/elisacore_std/elisacore_runtime.elisa")
	source := fmt.Sprintf("include %s\ninclude %s\n", strconv.Quote(runtimeInclude), strconv.Quote(include)) + body
	if err := os.WriteFile(input, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-O0", "-o", object, input}, &stdout, &stderr); code != 0 {
		t.Fatalf("compile actual collections runtime: exit %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	if output, err := compileNativeTestExecutable(t, clang, directory, []string{"-pthread", object, "-o", binary}); err != nil {
		t.Fatalf("link: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	if expectOverflow {
		if err == nil || !strings.Contains(string(output), "arena size multiplication overflow") {
			t.Fatalf("overflow did not reject before raw write: %v\n%s", err, output)
		}
	} else if err != nil {
		t.Fatalf("inactive bucket native control failed: %v\n%s", err, output)
	}
}

func TestActualDictInactiveStorageNativeInsertDeleteRehashAndClear(t *testing.T) {
	runActualDictInactiveNative(t, `struct Payload:
    owner: i64&
    text: sview

global anchor: i64 = 73

def main() -> int:
    can Memory.Allocate, Memory.Release, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        arena: mutable Arena = zeroed
        defer block:
            arena_free(&arena)
        payload: Payload = Payload{owner: &anchor, text: sview("payload", 0, -1)}
        key: cstr = "alpha"
        sentinel: mutable DictBucket[cstr, Payload] = DictBucket[cstr, Payload]{key: key, value: payload, state: 1}
        arena_dict_zero_buckets(&sentinel, 0)
        assert sentinel.state == 1
        assert sentinel.key == "alpha"
        assert sentinel.value.owner[0] == 73
        map: mutable dict[cstr, Payload] = arena_dict_new[cstr, Payload](&arena, 8)
        assert map.items != null
        initial_items: DictBucket[cstr, Payload]& = map.items
        for i in 0..<map.capacity:
            assert initial_items[i].state == 0
        assert arena_dict_get(&map, key) == null
        _ = arena_dict_put_or_panic(&arena, &map, key, payload)
        _ = arena_dict_put_or_panic(&arena, &map, "beta", payload)
        fetched: Payload&? = arena_dict_get(&map, key)
        assert fetched != null
        live: Payload& = fetched
        assert live.owner[0] == 73
        assert live.text.len == 7
        assert arena_dict_remove(&map, key)
        assert arena_dict_get(&map, key) == null
        tombstones: mutable usize = 0
        assert map.items != null
        removed_items: DictBucket[cstr, Payload]& = map.items
        for i in 0..<map.capacity:
            tombstones <- tombstones + 1 if removed_items[i].state == 2
        assert tombstones == 1
        _ = arena_dict_put_or_panic(&arena, &map, key, payload)
        arena_dict_reserve_or_panic(&arena, &map, 64)
        assert map.capacity >= 64
        assert map.count == 2
        after: Payload&? = arena_dict_get(&map, key)
        assert after != null
        restored: Payload& = after
        assert restored.owner[0] == 73
        assert restored.text.len == 7
        seen: mutable usize = 0
        for entry in map:
            assert entry.value.owner[0] == 73
            assert entry.value.text.len == 7
            seen <- seen + 1
        assert seen == 2
        arena_dict_clear(&map)
        assert map.count == 0 and map.used == 0
        assert arena_dict_get(&map, key) == null
        assert map.items != null
        cleared_items: DictBucket[cstr, Payload]& = map.items
        for i in 0..<map.capacity:
            assert cleared_items[i].state == 0
        _ = arena_dict_put_or_panic(&arena, &map, key, payload)
        assert arena_dict_get(&map, key) != null
        return 0
`, false)
}

func TestActualDictInactiveStorageOverflowRejectsBeforeWrite(t *testing.T) {
	runActualDictInactiveNative(t, `struct Payload:
    owner: i64&
    text: sview

global anchor: i64 = 73

def main() -> int:
    can Abort.Panic, Global.Read:
        payload: Payload = Payload{owner: &anchor, text: sview("payload", 0, -1)}
        bucket: mutable DictBucket[cstr, Payload] = DictBucket[cstr, Payload]{key: "key", value: payload, state: 1}
        arena_dict_zero_buckets(&bucket, 9223372036854775807.usize())
        return 9
`, true)
}
