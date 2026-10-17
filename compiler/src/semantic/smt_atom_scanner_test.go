package semantic

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func referenceSMTAtoms(sexpr string) []string {
	var atoms []string
	i := 0
	for i < len(sexpr) {
		c := sexpr[i]
		switch {
		case c == '(' || c == ')' || c == ' ' || c == '\n' || c == '\t' || c == '\r':
			i++
		case c == '|':
			j := strings.IndexByte(sexpr[i+1:], '|')
			if j < 0 {
				return atoms
			}
			atoms = append(atoms, sexpr[i:i+j+2])
			i += j + 2
		case c == '"':
			j := i + 1
			for j < len(sexpr) && sexpr[j] != '"' {
				j++
			}
			i = j + 1
		default:
			j := i
			for j < len(sexpr) && !strings.ContainsRune("() \n\t\r|\"", rune(sexpr[j])) {
				j++
			}
			atoms = append(atoms, sexpr[i:j])
			i = j
		}
	}
	return atoms
}

func TestSMTAtomScannerMatchesPreviousCollector(t *testing.T) {
	inputs := []string{"", "(assert (= |quoted symbol| 1))", `(echo "a (b)""c")`, "|unterminated", "abc |unterminated", "(assert (= π 中文))", "\"unterminated", "(x)\r\n\t(y)", "(x ; comment)"}
	random := rand.New(rand.NewSource(818))
	alphabet := []byte("abc|\"() \n\t\r;012\xff\xc2")
	for i := 0; i < 2000; i++ {
		data := make([]byte, random.Intn(256))
		for j := range data {
			data[j] = alphabet[random.Intn(len(alphabet))]
		}
		inputs = append(inputs, string(data))
	}
	for _, input := range inputs {
		var got []string
		scan := smtAtomScanner{text: input}
		for atom, present := scan.next(); present; atom, present = scan.next() {
			got = append(got, atom)
		}
		if want := referenceSMTAtoms(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("input %q got %q want %q", input, got, want)
		}
		if _, present := scan.next(); present {
			t.Fatal("exhausted scanner revived")
		}
	}
}
