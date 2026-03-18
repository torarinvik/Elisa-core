package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

type syntheticJSONCase struct {
	name   string
	depth  int
	width  int
	repeat int
}

var namedCases = map[string]syntheticJSONCase{
	"small":  {name: "small", depth: 3, width: 2, repeat: 2},
	"medium": {name: "medium", depth: 4, width: 3, repeat: 2},
	"large":  {name: "large", depth: 5, width: 3, repeat: 2},
}

func main() {
	caseName := flag.String("case", "large", "named corpus case: small, medium, or large")
	depth := flag.Int("depth", -1, "JSON object nesting depth override")
	width := flag.Int("width", -1, "number of children/attrs/tags per node override")
	repeat := flag.Int("repeat", -1, "number of top-level repeated roots override")
	outputPath := flag.String("o", "", "write output to this file instead of stdout")
	flag.Parse()

	tc, ok := namedCases[*caseName]
	if !ok {
		fatalf("unknown -case %q (expected one of: small, medium, large)", *caseName)
	}
	if *depth >= 0 {
		tc.depth = *depth
	}
	if *width >= 0 {
		tc.width = *width
	}
	if *repeat >= 0 {
		tc.repeat = *repeat
	}
	if tc.depth < 0 || tc.width < 0 || tc.repeat < 0 {
		fatalf("depth, width, and repeat must all be non-negative")
	}

	payload := buildSyntheticJSONCorpus(tc)
	if *outputPath == "" {
		if _, err := os.Stdout.Write(payload); err != nil {
			fatalf("failed to write corpus to stdout: %v", err)
		}
		return
	}
	if err := os.WriteFile(*outputPath, payload, 0o644); err != nil {
		fatalf("failed to write corpus to %s: %v", *outputPath, err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
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
