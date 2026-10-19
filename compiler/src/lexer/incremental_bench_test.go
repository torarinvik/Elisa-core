package lexer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Time lexer ownership/copying work, excluding edit creation and parity setup.
// Parser token/AST copies and semantic analysis belong to separate measurements.
func benchmarkSourceRevisions(b *testing.B, filename string, previous *SourceTokens, revisions [][]byte, retained bool) {
	var old *SourceTokens
	if retained {
		old = previous
	}
	for _, source := range revisions {
		got := TokenizeSource(filename, source, old)
		cold := New(filename, source)
		if !reflect.DeepEqual(got.CopyTokens(), cold.Tokenize()) || !reflect.DeepEqual(got.CopyErrors(), cold.Errors()) {
			b.Fatal("incremental tokens/diagnostics differ from cold lexer")
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(revisions[0])))
	b.ResetTimer()
	var prefix, suffix, tokens int64
	var last *SourceTokens
	for i := 0; i < b.N; i++ {
		got := TokenizeSource(filename, revisions[i%len(revisions)], old)
		prefix += int64(got.ReusedTokens())
		suffix += int64(got.SuffixReusedTokens())
		tokens += int64(len(got.tokens))
		last = got
	}
	b.StopTimer()
	if last != nil {
		size, ok := last.RetainedBytes(int(^uint(0) >> 1))
		if !ok {
			b.Fatal("retained payload accounting overflow")
		}
		b.ReportMetric(float64(size), "retained-B/op")
		b.ReportMetric(float64(cap(last.tokens)), "token-cap/op")
	}
	b.ReportMetric(float64(prefix)/float64(b.N), "prefix-reused/op")
	b.ReportMetric(float64(suffix)/float64(b.N), "suffix-reused/op")
	b.ReportMetric(float64(tokens-prefix-suffix)/float64(b.N), "scanned-tokens/op")
}

func BenchmarkSourceComponentEdits(b *testing.B) {
	for _, size := range []int{8 << 10, 64 << 10, 512 << 10} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			source := []byte(strings.Repeat("value <- 3 + 4 * 5\n", size/len("value <- 3 + 4 * 5\n")))
			previous := TokenizeSource("component.elisa", source, nil)
			for _, edit := range []struct {
				name   string
				offset int
			}{
				{"unchanged", -1}, {"early", 0}, {"middle", len(source) / 2}, {"late", len(source) - 128},
			} {
				revisions := [][]byte{source}
				if edit.offset >= 0 {
					offset := edit.offset
					if offset > 0 {
						offset += bytes.IndexByte(source[offset:], '\n') + 1
					}
					revisions = make([][]byte, 4)
					for i := range revisions {
						inserted := []byte(fmt.Sprintf("seed <- %d\n\n", 10000+i))
						revisions[i] = append(append(append(make([]byte, 0, len(source)+len(inserted)), source[:offset]...), inserted...), source[offset:]...)
					}
				}
				b.Run(edit.name, func(b *testing.B) {
					for _, retained := range []bool{false, true} {
						mode := "cold"
						if retained {
							mode = "retained"
						}
						b.Run(mode, func(b *testing.B) { benchmarkSourceRevisions(b, "component.elisa", previous, revisions, retained) })
					}
				})
			}
		})
	}
}

// Use an existing frontend implementation fixture as a varied source corpus,
// including indentation, generics, optional references and multi-line signatures.
func BenchmarkSourceFrontendStressEdits(b *testing.B) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "Code", "test_programs", "frontend_stress.elisa"))
	if err != nil {
		b.Fatal(err)
	}
	previous := TokenizeSource("frontend_stress.elisa", source, nil)
	cases := []struct {
		name          string
		before, after []byte
	}{
		{"unchanged", nil, nil},
		{"insert-comment", []byte("def parser_peek"), []byte("# edited source revision\n\ndef parser_peek")},
		{"replace-number", []byte("state.cursor == 0"), []byte("state.cursor == 12345")},
		{"delete-declaration", []byte("error RuntimeError:\n    OutOfMemory\n\n"), nil},
		{"delete-indent", []byte("    if state.cursor == 0:"), []byte("if state.cursor == 0:")},
	}
	for _, edit := range cases {
		revised := source
		if edit.before != nil {
			if bytes.Count(source, edit.before) != 1 {
				b.Fatalf("fixture marker %q is not unique", edit.before)
			}
			revised = bytes.Replace(source, edit.before, edit.after, 1)
		}
		b.Run(edit.name, func(b *testing.B) {
			for _, retained := range []bool{false, true} {
				mode := "cold"
				if retained {
					mode = "retained"
				}
				b.Run(mode, func(b *testing.B) {
					benchmarkSourceRevisions(b, "frontend_stress.elisa", previous, [][]byte{revised}, retained)
				})
			}
		})
	}
}

func BenchmarkSourceComponentFallbacks(b *testing.B) {
	source := []byte(strings.Repeat("value <- 3 + 4 * 5\n", 3600))
	previous := TokenizeSource("fallback.elisa", source, nil)
	for _, edit := range []struct {
		name   string
		source []byte
	}{
		{"rewrite", bytes.ReplaceAll(source, []byte("value"), []byte("other"))},
		{"large-shrink", source[len(source)-1024:]},
		{"repeated-prefix-shrink", source[len(source)/4:]},
		{"unterminated-comment", append([]byte("\"\"\""), source...)},
		{"middle-unterminated-comment", append(append(append([]byte(nil), source[:len(source)/2]...), []byte("\"\"\"")...), source[len(source)/2:]...)},
	} {
		b.Run(edit.name, func(b *testing.B) {
			for _, retained := range []bool{false, true} {
				mode := "cold"
				if retained {
					mode = "retained"
				}
				b.Run(mode, func(b *testing.B) {
					benchmarkSourceRevisions(b, "fallback.elisa", previous, [][]byte{edit.source}, retained)
				})
			}
		})
	}
}

func BenchmarkSourceComponentDistinctDeletions(b *testing.B) {
	var fixture strings.Builder
	for i := 0; i < 3600; i++ {
		fmt.Fprintf(&fixture, "value_%d <- 3 + 4 * 5\n", i)
	}
	source := []byte(fixture.String())
	previous := TokenizeSource("deletions.elisa", source, nil)
	for _, edit := range []struct {
		name   string
		source []byte
	}{
		{"prefix-quarter", source[len(source)/4:]},
		{"tail-quarter", source[:3*len(source)/4]},
		{"middle-quarter", append(append([]byte(nil), source[:len(source)/2]...), source[3*len(source)/4:]...)},
	} {
		b.Run(edit.name, func(b *testing.B) {
			for _, retained := range []bool{false, true} {
				mode := "cold"
				if retained {
					mode = "retained"
				}
				b.Run(mode, func(b *testing.B) {
					benchmarkSourceRevisions(b, "deletions.elisa", previous, [][]byte{edit.source}, retained)
				})
			}
		})
	}
}
