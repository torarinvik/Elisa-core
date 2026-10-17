package semantic

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestSMTTokenScannerPreservesFieldsFunc(t *testing.T) {
	inputs := []string{"", "(x\ry)\t z", "|quoted name|", "\"string text\"", "中文 π\xff"}
	random := rand.New(rand.NewSource(215))
	alphabet := []byte("abc() \n\t\r|\";\xff\xc2")
	for i := 0; i < 2000; i++ {
		data := make([]byte, random.Intn(256))
		for j := range data {
			data[j] = alphabet[random.Intn(len(alphabet))]
		}
		inputs = append(inputs, string(data))
	}
	for _, input := range inputs {
		want := strings.FieldsFunc(input, func(r rune) bool { return r == ' ' || r == '(' || r == ')' || r == '\n' || r == '\t' })
		got := []string{}
		scan := smtTokenScanner{text: input}
		for token, ok := scan.next(); ok; token, ok = scan.next() {
			got = append(got, token)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input=%q got=%q want=%q", input, got, want)
		}
	}
}
