package semantic

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSMTViewLengthSymbolsPreserveEveryIdentityByte(t *testing.T) {
	values := []string{"", "a", "aa", "a\x00", "a|b\\c", "module::field.name", "Æβ\n"}
	for i := 0; i < 256; i++ {
		values = append(values, string([]byte{byte(i)}))
	}
	seen := map[string]string{}
	for _, value := range values {
		symbol := smtSViewLengthSymbol(value)
		if prior, ok := seen[symbol]; ok && prior != value {
			t.Fatal("distinct view identities alias")
		}
		seen[symbol] = value
		encoded := strings.TrimSuffix(strings.TrimPrefix(symbol, "|sview_length_"), "|")
		if strings.ContainsAny(encoded, "|\\") {
			t.Fatal("symbol contains an SMT quoted-name delimiter")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != value {
			t.Fatalf("identity did not round-trip: %q", value)
		}
	}
}

func TestSMTViewLengthEncodingPreservesAlphaFormula(t *testing.T) {
	identity := "sview-len:\x00/long/source/path/function.elisa:344:36915:param:0"
	old := "|sview_length_" + hex.EncodeToString([]byte(identity)) + "|"
	current := smtSViewLengthSymbol(identity)
	render := func(name string) string {
		return "(declare-const " + name + " Int)\n(assert (>= " + name + " 0))\n(assert (not (< " + name + " 9223372036854775808)))\n"
	}
	before, bok := smtAlphaQuery(render(old))
	after, aok := smtAlphaQuery(render(current))
	if !bok || !aok || before != after {
		t.Fatal("encoding changed the alpha-normalized formula")
	}
	if len(current) >= len(old) {
		t.Fatal("production-shaped identity did not shrink")
	}
}

var viewLengthSymbolSink string

func BenchmarkSMTViewLengthSymbol(b *testing.B) {
	identity := "sview-len:\x00/Users/developer/Documents/Coding Projects/Compiler/src/backend/easm_operand_scan.elisa:344:36915:param:0"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		viewLengthSymbolSink = smtSViewLengthSymbol(identity)
	}
}
