package semantic

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf8"
)

func codecLexicalSpec() lexicalSelectionSpec {
	spec := lexicalSelectionSpec{Site: FunctionSourceAnchor{FunctionKey: "function-v1:" + strings.Repeat("a", 64), Kind: "*ast.Ident", Ordinal: 42}, Role: "primary-identifier-scope-read", Domain: "symbol", Name: "value"}
	for i := range spec.Site.Tree {
		spec.Site.Tree[i] = byte(i * 7)
	}
	return spec
}

func TestLexicalKeyRoundTripAndAllDependencies(t *testing.T) {
	base := codecLexicalSpec()
	variants := []lexicalSelectionSpec{base}
	for i := 0; i < 7; i++ {
		changed := base
		switch i {
		case 0:
			changed.Site.FunctionKey += "/other"
		case 1:
			changed.Site.Tree[31] ^= 128
		case 2:
			changed.Site.Ordinal = ^uint32(0)
		case 3:
			changed.Site.Kind = "*ast.ReturnStmt"
		case 4:
			changed.Role += "/other"
		case 5:
			changed.Domain = "local-symbol"
		case 6:
			changed.Name = "λ\x00nested"
		}
		variants = append(variants, changed)
	}
	seen := map[string]bool{}
	for _, spec := range variants {
		key, err := lexicalSelectionKey(spec)
		if err != nil || !utf8.ValidString(key.Name) || seen[key.Name] {
			t.Fatalf("invalid or conflated dependency key: %v", err)
		}
		seen[key.Name] = true
		decoded, err := decodeLexicalSelectionKey(key.Name)
		if err != nil || decoded != spec {
			t.Fatalf("round trip changed dependencies: got %+v, want %+v: %v", decoded, spec, err)
		}
		if repeated, err := lexicalSelectionKey(spec); err != nil || repeated != key {
			t.Fatal("key encoding is not deterministic")
		}
	}
}

func TestLexicalKeyRejectsMalformedAndNoncanonicalInputs(t *testing.T) {
	key, err := lexicalSelectionKey(codecLexicalSpec())
	if err != nil {
		t.Fatal(err)
	}
	for end := 0; end < len(key.Name); end++ {
		if _, err := decodeLexicalSelectionKey(key.Name[:end]); err == nil {
			t.Fatalf("truncated key accepted at %d", end)
		}
	}
	for _, name := range []string{key.Name + "=", key.Name + "\n", "L1:" + key.Name[3:], "{}", lexicalKeyPrefix + strings.Repeat("A", lookupSelectionMaxBytes)} {
		if _, err := decodeLexicalSelectionKey(name); err == nil {
			t.Fatal("noncanonical key accepted")
		}
	}
	data, _ := base64.RawURLEncoding.DecodeString(key.Name[len(lexicalKeyPrefix):])
	for _, mutation := range []string{"length", "trailing", "zero-tree", "invalid-utf8"} {
		changed := append([]byte(nil), data...)
		switch mutation {
		case "length":
			binary.LittleEndian.PutUint32(changed[36:40], ^uint32(0))
		case "trailing":
			changed = append(changed, 0)
		case "zero-tree":
			clear(changed[:32])
		case "invalid-utf8":
			changed[len(changed)-1] = 255
		}
		name := lexicalKeyPrefix + base64.RawURLEncoding.EncodeToString(changed)
		if _, err := decodeLexicalSelectionKey(name); err == nil {
			t.Fatal("malformed payload accepted:", mutation)
		}
	}
}

func TestLexicalKeyRejectsInvalidDependencies(t *testing.T) {
	for _, change := range []func(*lexicalSelectionSpec){
		func(s *lexicalSelectionSpec) { s.Site.FunctionKey = "" },
		func(s *lexicalSelectionSpec) { s.Site.Tree = [32]byte{} },
		func(s *lexicalSelectionSpec) { s.Site.Kind = "" },
		func(s *lexicalSelectionSpec) { s.Role = "" },
		func(s *lexicalSelectionSpec) { s.Domain = "unknown" },
		func(s *lexicalSelectionSpec) { s.Name = "" },
		func(s *lexicalSelectionSpec) { s.Name = string([]byte{255}) },
		func(s *lexicalSelectionSpec) { s.Name = strings.Repeat("x", lookupSelectionMaxBytes) },
	} {
		spec := codecLexicalSpec()
		change(&spec)
		if _, err := lexicalSelectionKey(spec); err == nil {
			t.Fatal("invalid dependency identity accepted")
		}
	}
}

func FuzzLexicalSelectionKeyCanonical(f *testing.F) {
	key, err := lexicalSelectionKey(codecLexicalSpec())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(key.Name)
	f.Add("{}")
	f.Add(lexicalKeyPrefix)
	f.Fuzz(func(t *testing.T, encoded string) {
		spec, err := decodeLexicalSelectionKey(encoded)
		if err != nil {
			return
		}
		key, err := lexicalSelectionKey(spec)
		if err != nil || key.Name != encoded {
			t.Fatalf("accepted a noncanonical identity: %v", err)
		}
	})
}

func BenchmarkLexicalSelectionKey(b *testing.B) {
	spec := codecLexicalSpec()
	key, err := lexicalSelectionKey(spec)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := lexicalSelectionKey(spec); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := decodeLexicalSelectionKey(key.Name); err != nil {
				b.Fatal(err)
			}
		}
	})
}
