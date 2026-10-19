package main

import (
	"bytes"
	"crypto/sha256"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"reflect"
	"runtime"
	"testing"
)

func TestSourceTokenViewOutlivesEviction(t *testing.T) {
	const original = "#line 70 original.elisa\ndef value() -> i64:\n    return 21\n"
	input := []byte(original)
	cache := newSourceTokenCache(1, 1<<20)
	key := sourceTokenKey{"view.elisa", sha256.Sum256(input)}
	view, errors := cache.lexBorrowedWithKey(key, input)
	if len(errors) != 0 {
		t.Fatal(errors)
	}
	for i := range input {
		input[i] = '?'
	}
	cache.lex("other.elisa", []byte("def other():\n    pass\n"))
	if _, ok := cache.entries[key]; ok {
		t.Fatal("fixture did not evict source entry")
	}
	lexer.DropParitySource("view.elisa")
	runtime.GC()
	p := parser.NewWithBorrowedTokens(view)
	actual := p.ParseFile("view.elisa")
	cold := parser.New(lexer.New("view.elisa", []byte(original)).Tokenize())
	expected := cold.ParseFile("view.elisa")
	if !reflect.DeepEqual(actual, expected) || len(p.Errors()) != 0 {
		t.Fatal("borrowed view lost its source or tokens after cache eviction")
	}
}

func TestSourceTokenViewRecoveryDoesNotPoisonCache(t *testing.T) {
	source := []byte("def work():\n    to <- 1\n    for tail in [1, 2]:\n        pass\n")
	cache := newSourceTokenCache(2, 1<<20)
	var cold bytes.Buffer
	if _, ok := parseProgramWithTokenCache("recover.elisa", source, &cold, nil); ok {
		t.Fatal("fixture did not trigger reserved-word recovery")
	}
	pristine := lexer.New("recover.elisa", source).Tokenize()
	for i := 0; i < 3; i++ {
		var diagnostics bytes.Buffer
		if _, ok := parseProgramWithTokenCache("recover.elisa", source, &diagnostics, cache); ok || diagnostics.String() != cold.String() {
			t.Fatalf("cached recovery changed diagnostics on request %d: %s", i, diagnostics.String())
		}
		for _, entry := range cache.entries {
			if !reflect.DeepEqual(entry.lexed.CopyTokens(), pristine) {
				t.Fatal("recovery rewrote shared cached tokens")
			}
		}
	}
	if cache.hits == 0 {
		t.Fatal("fixture did not exercise a token-cache hit")
	}
}
