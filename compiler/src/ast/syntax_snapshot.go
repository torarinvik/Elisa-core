package ast

import (
	"fmt"
	"reflect"
	"runtime"
)

// SyntaxSnapshot owns a pristine parser graph. Copies preserve node/container
// sharing and map-key identities while exposing no mutable snapshot storage.
// Capture requires a stable input graph; overlapping interior/partial views are
// declined rather than silently changing their alias semantics.
type SyntaxSnapshot struct {
	file  *File
	bytes int
}

// RetainedBytes estimates retained graph payload, including container overhead; it is not an RSS bound.
func (s *SyntaxSnapshot) RetainedBytes() int { return s.bytes }

func NewSyntaxSnapshot(file *File, limit int) (*SyntaxSnapshot, error) {
	if !syntaxFastSchemaValid {
		return nil, fmt.Errorf("syntax replay schema requires regeneration")
	}
	if file == nil {
		return nil, fmt.Errorf("missing syntax file")
	}
	c := &syntaxFastCopier{own: true, limit: limit, nodes: map[any]any{}, slices: map[syntaxFastSlice]any{}, maps: map[syntaxFastMap]any{}, text: map[string]string{}}
	c.charge(int(reflect.TypeOf(SyntaxSnapshot{}).Size()))
	value := captureSyntaxFile(c, file)
	if c.err == nil {
		c.checkStorage()
	}
	runtime.KeepAlive(file)
	if c.err != nil {
		return nil, c.err
	}
	return &SyntaxSnapshot{file: value, bytes: c.bytes}, nil
}
func (s *SyntaxSnapshot) CopyFile() (*File, error) {
	if s == nil || s.file == nil {
		return nil, fmt.Errorf("missing syntax snapshot")
	}
	if !syntaxFastSchemaValid {
		return nil, fmt.Errorf("syntax replay schema requires regeneration")
	}
	file, err := copyFastSyntaxFile(s.file)
	runtime.KeepAlive(s)
	if err != nil {
		return nil, err
	}
	return file, nil
}
