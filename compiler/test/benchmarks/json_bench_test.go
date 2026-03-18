package benchmarks_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type syntheticJSONCase struct {
	name   string
	depth  int
	width  int
	repeat int
}

var syntheticJSONCases = []syntheticJSONCase{
	{name: "small", depth: 3, width: 2, repeat: 2},
	{name: "medium", depth: 4, width: 3, repeat: 2},
	{name: "large", depth: 5, width: 3, repeat: 2},
}

func TestSyntheticJSONCorpusIsValid(t *testing.T) {
	for _, tc := range syntheticJSONCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := buildSyntheticJSONCorpus(tc)
			var decoded any
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatalf("json.Unmarshal failed: %v\npayload prefix: %s", err, previewBytes(payload, 200))
			}
			if checksumJSONValue(decoded) == 0 {
				t.Fatalf("expected non-zero checksum for %s corpus", tc.name)
			}
		})
	}
}

func BenchmarkEncodingJSONParseSyntheticCorpus(b *testing.B) {
	for _, tc := range syntheticJSONCases {
		payload := buildSyntheticJSONCorpus(tc)
		b.Run(tc.name, func(b *testing.B) {
			benchmarkEncodingJSONParse(b, payload)
		})
	}
}

func benchmarkEncodingJSONParse(b *testing.B, payload []byte) {
	b.Helper()
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()

	checksum := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			b.Fatalf("json.Unmarshal failed: %v", err)
		}
		checksum += checksumJSONValue(decoded)
	}
	if checksum == 0 {
		b.Fatal("expected benchmark checksum to stay non-zero")
	}
}

func buildSyntheticJSONCorpus(tc syntheticJSONCase) []byte {
	var builder strings.Builder
	builder.Grow(1 << 16)
	builder.WriteByte('[')
	for i := 0; i < tc.repeat; i++ {
		if i != 0 {
			builder.WriteByte(',')
		}
		appendSyntheticJSONNode(&builder, tc.depth, tc.width, i+1)
	}
	builder.WriteByte(']')
	return []byte(builder.String())
}

func appendSyntheticJSONNode(builder *strings.Builder, depth int, width int, seed int) {
	builder.WriteByte('{')
	writeJSONFieldName(builder, "id")
	builder.WriteString(fmt.Sprintf("%d", seed))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "kind")
	builder.WriteString(fmt.Sprintf("\"node-%d\"", depth))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "enabled")
	if seed%2 == 0 {
		builder.WriteString("true")
	} else {
		builder.WriteString("false")
	}
	builder.WriteByte(',')
	writeJSONFieldName(builder, "weight")
	builder.WriteString(fmt.Sprintf("%d", seed*depth+width))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "name")
	builder.WriteString(fmt.Sprintf("\"node-%d-%d\"", depth, seed))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "meta")
	appendSyntheticJSONMeta(builder, depth, width, seed)
	builder.WriteByte(',')
	writeJSONFieldName(builder, "children")
	if depth <= 0 {
		builder.WriteString("[]")
	} else {
		builder.WriteByte('[')
		for i := 0; i < width; i++ {
			if i != 0 {
				builder.WriteByte(',')
			}
			appendSyntheticJSONNode(builder, depth-1, width, seed*10+i+1)
		}
		builder.WriteByte(']')
	}
	builder.WriteByte('}')
}

func appendSyntheticJSONMeta(builder *strings.Builder, depth int, width int, seed int) {
	builder.WriteByte('{')
	writeJSONFieldName(builder, "depth")
	builder.WriteString(fmt.Sprintf("%d", depth))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "width")
	builder.WriteString(fmt.Sprintf("%d", width))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "seed")
	builder.WriteString(fmt.Sprintf("%d", seed))
	builder.WriteByte(',')
	writeJSONFieldName(builder, "tags")
	builder.WriteByte('[')
	for i := 0; i < width; i++ {
		if i != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(fmt.Sprintf("\"tag-%d-%d\"", depth, i))
	}
	builder.WriteByte(']')
	builder.WriteByte(',')
	writeJSONFieldName(builder, "attrs")
	builder.WriteByte('{')
	for i := 0; i < width; i++ {
		if i != 0 {
			builder.WriteByte(',')
		}
		writeJSONFieldName(builder, fmt.Sprintf("k%d", i))
		builder.WriteString(fmt.Sprintf("%d", seed+i+depth))
	}
	builder.WriteByte('}')
	builder.WriteByte('}')
}

func writeJSONFieldName(builder *strings.Builder, name string) {
	builder.WriteByte('"')
	builder.WriteString(name)
	builder.WriteString(`":`)
}

func checksumJSONValue(value any) int {
	switch n := value.(type) {
	case nil:
		return 1
	case bool:
		if n {
			return 7
		}
		return 3
	case float64:
		return int(n) + 11
	case string:
		return len(n) + 13
	case []any:
		total := 17 + len(n)
		for _, item := range n {
			total += checksumJSONValue(item)
		}
		return total
	case map[string]any:
		total := 19 + len(n)
		for key, item := range n {
			total += len(key) + checksumJSONValue(item)
		}
		return total
	default:
		return 23
	}
}

func previewBytes(data []byte, max int) string {
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + "..."
}
