package main

import (
	"bytes"
	"strconv"
)

// sourceSplice borrows immutable spans of files while expanding includes.
// Reading a whole expansion allocates its final byte slice once. Callers
// appending to an existing buffer can use the same expansion with that sink.
type sourceSplice struct {
	buffer *bytes.Buffer
	spans  [][]byte
	size   int
}

var sourceSpliceNewline = []byte{'\n'}

func (s *sourceSplice) write(span []byte) {
	if len(span) == 0 {
		return
	}
	if s.buffer != nil {
		s.buffer.Write(span)
		return
	}
	s.spans = append(s.spans, span)
	s.size += len(span)
}

func (s *sourceSplice) len() int {
	if s.buffer != nil {
		return s.buffer.Len()
	}
	return s.size
}

func (s *sourceSplice) endsInNewline() bool {
	if s.buffer != nil {
		return s.buffer.Bytes()[s.buffer.Len()-1] == '\n'
	}
	last := s.spans[len(s.spans)-1]
	return last[len(last)-1] == '\n'
}

func (s *sourceSplice) lineDirective(line int, file string) {
	directive := make([]byte, 0, len(file)+24)
	directive = append(directive, "#line "...)
	directive = strconv.AppendInt(directive, int64(line), 10)
	directive = append(directive, ' ')
	directive = append(directive, file...)
	directive = append(directive, '\n')
	s.write(directive)
}

// Indented includes can emit several short spans per line. Switch to a
// buffered sink before entering that subtree to bound span metadata.
func (s *sourceSplice) useBuffer() {
	if s.buffer != nil {
		return
	}
	s.buffer = bytes.NewBuffer(s.bytes())
	s.spans = nil
}

func (s *sourceSplice) bytes() []byte {
	if s.buffer != nil {
		return s.buffer.Bytes()
	}
	if s.size == 0 {
		return nil
	}
	out := make([]byte, 0, s.size)
	for _, span := range s.spans {
		out = append(out, span...)
	}
	return out
}
