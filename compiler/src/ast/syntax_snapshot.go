package ast

import (
	"fmt"
	"math/bits"
	"reflect"
	"runtime"
	"sync"
)

// SyntaxSnapshot owns a pristine parser graph. Copies preserve node/container
// sharing and map-key identities while exposing no mutable snapshot storage.
// Capture requires a stable input graph; overlapping interior/partial views are
// declined rather than silently changing their alias semantics.
type SyntaxSnapshot struct {
	file                *File
	bytes               int
	nodes, slices, maps int
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
	return &SyntaxSnapshot{file: value, bytes: c.bytes, nodes: len(c.nodes), slices: len(c.slices), maps: len(c.maps)}, nil
}
func (s *SyntaxSnapshot) CopyFile() (*File, error) {
	if s == nil || s.file == nil {
		return nil, fmt.Errorf("missing syntax snapshot")
	}
	if !syntaxFastSchemaValid {
		return nil, fmt.Errorf("syntax replay schema requires regeneration")
	}
	c, nodeBucket := s.replayCopier(len(s.file.Decls))
	defer releaseSyntaxReplayNodes(c.nodes, nodeBucket)
	file := copyFastSyntaxFileInto(c, s.file)
	err := c.err
	runtime.KeepAlive(s)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// CopyPrefix returns a fresh pristine declaration prefix, with visibility keys
// rebound only to declarations reachable from that prefix. Its strings remain
// backed by the immutable owned snapshot and carry their unchanged positions.
func (s *SyntaxSnapshot) CopyPrefix(declarations int) (*File, error) {
	return s.copyDeclarationRange(0, declarations)
}

// CopySuffix owns the remaining declarations and rebinds their visibility keys.
func (s *SyntaxSnapshot) CopySuffix(declarations int) (*File, error) {
	if s == nil || s.file == nil {
		return nil, fmt.Errorf("invalid syntax suffix")
	}
	return s.copyDeclarationRange(declarations, len(s.file.Decls))
}

func (s *SyntaxSnapshot) copyDeclarationRange(start, end int) (*File, error) {
	if s == nil || s.file == nil || !syntaxFastSchemaValid || start < 0 || end < start || end > len(s.file.Decls) {
		return nil, fmt.Errorf("invalid syntax prefix")
	}
	header := *s.file
	header.Decls = header.Decls[start:end:end]
	header.DeclVisibility = nil
	c, nodeBucket := s.replayCopier(end - start)
	defer releaseSyntaxReplayNodes(c.nodes, nodeBucket)
	file := copyFastSyntaxFileInto(c, &header)
	if s.file.DeclVisibility != nil {
		file.DeclVisibility = map[Decl]string{}
		for key, value := range s.file.DeclVisibility {
			// Current declaration nodes have pointer identity. Refuse a future value
			// implementation rather than silently losing its visibility metadata.
			if key == nil || reflect.TypeOf(key).Kind() != reflect.Pointer {
				return nil, fmt.Errorf("unsupported syntax visibility identity")
			}
			if copied, ok := c.nodes[key]; ok {
				file.DeclVisibility[copied.(Decl)] = value
			}
		}
	}
	runtime.KeepAlive(s)
	if c.err != nil {
		return nil, c.err
	}
	return file, nil
}

// Replay reserves temporary identity tables from capture statistics. Partial
// ranges use a conservative average per declaration; this is a capacity hint
// only and does not authorize skipping any graph traversal or alias checks.
func (s *SyntaxSnapshot) replayCopier(declarations int) (*syntaxFastCopier, int) {
	estimate := func(count int) int {
		total := len(s.file.Decls)
		if total == 0 || declarations >= total {
			return count
		}
		// Multiply before dividing so partial copies retain a useful estimate even
		// when there are fewer captured nodes than declarations. The product can
		// exceed int, so use the 128-bit helpers; the quotient is at most count.
		hi, lo := bits.Mul64(uint64(count), uint64(declarations))
		capacity, remainder := bits.Div64(hi, lo, uint64(total))
		if remainder != 0 && capacity < uint64(count) {
			capacity++
		}
		return int(capacity)
	}
	nodes, bucket := borrowSyntaxReplayNodes(estimate(s.nodes))
	return &syntaxFastCopier{nodes: nodes, slices: make(map[syntaxFastSlice]any, estimate(s.slices)), maps: make(map[syntaxFastMap]any, estimate(s.maps))}, bucket
}

// CopyDeclaration returns a fresh pristine copy of declaration i. The snapshot is
// immutable, so concurrent copies are safe.
func (s *SyntaxSnapshot) CopyDeclaration(i int) (Decl, error) {
	file, err := s.copyDeclarationRange(i, i+1)
	if err != nil {
		return nil, err
	}
	return file.Decls[0], nil
}

// Temporary identity tables contain no returned storage. Size buckets avoid
// repeatedly allocating them without handing a large sparse table to tiny copies.
// Unusually large graphs keep the ordinary allocation path and are not pooled.
var syntaxReplayNodePools [12]sync.Pool // capacities 8 through 16K

func syntaxReplayNodeBucket(count int) int {
	if count < 0 || count > 1<<14 {
		return -1
	}
	if count <= 8 {
		return 0
	}
	return bits.Len(uint(count-1)) - 3
}

func borrowSyntaxReplayNodes(count int) (map[any]any, int) {
	bucket := syntaxReplayNodeBucket(count)
	if bucket < 0 {
		return make(map[any]any, count), -1
	}
	if cached := syntaxReplayNodePools[bucket].Get(); cached != nil {
		return cached.(map[any]any), bucket
	}
	return make(map[any]any, 8<<bucket), bucket
}

func releaseSyntaxReplayNodes(nodes map[any]any, bucket int) {
	if bucket < 0 {
		return
	}
	grownBucket := syntaxReplayNodeBucket(len(nodes))
	if grownBucket < 0 {
		return
	}
	// A partial copy can contain fewer nodes than the estimate. Keep the
	// original bucket so its potentially large capacity never enters a tiny pool.
	bucket = max(bucket, grownBucket)
	clear(nodes)
	syntaxReplayNodePools[bucket].Put(nodes)
}
