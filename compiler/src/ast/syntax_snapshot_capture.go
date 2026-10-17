package ast

import (
	"fmt"
	"sort"
	"strings"
	"unsafe"
)

type syntaxStorage struct{ start, end uintptr }

// The generated capture visits the same typed schema as replay, but owns text,
// accounts storage, and certifies alias/work bounds before publication.
func (c *syntaxFastCopier) enter() bool {
	if c.err != nil {
		return false
	}
	c.work++
	c.depth++
	if c.work > 1_000_000 || c.depth > 2048 {
		c.err = fmt.Errorf("syntax snapshot work/depth budget exceeded")
		c.depth--
		return false
	}
	return true
}
func (c *syntaxFastCopier) leave() { c.depth-- }
func (c *syntaxFastCopier) charge(size int) bool {
	if c.err != nil {
		return false
	}
	if size < 0 || size > c.limit-c.bytes {
		c.err = fmt.Errorf("syntax snapshot byte budget exceeded")
		return false
	}
	c.bytes += size
	return true
}
func (c *syntaxFastCopier) captureStorage(pointer unsafe.Pointer, element uintptr, count int) bool {
	if !c.own {
		return true
	}
	if element == 0 && count > 0 {
		c.err = fmt.Errorf("zero-sized syntax identity requires cold parsing")
		return false
	}
	if element > 0 && uintptr(count) > uintptr(c.limit-c.bytes)/element {
		c.err = fmt.Errorf("syntax storage budget exceeded")
		return false
	}
	size := int(element * uintptr(count))
	if !c.charge(size) {
		return false
	}
	if size > 0 {
		start := uintptr(pointer)
		c.storage = append(c.storage, syntaxStorage{start, start + uintptr(size)})
	}
	return true
}
func (c *syntaxFastCopier) captureMap(element uintptr, count int) bool {
	if !c.own {
		return true
	}
	if !c.charge(128) {
		return false
	}
	if element > 0 && uintptr(count) > uintptr(c.limit-c.bytes)/(2*element) {
		c.err = fmt.Errorf("syntax map budget exceeded")
		return false
	}
	return c.charge(int(2 * element * uintptr(count)))
}
func (c *syntaxFastCopier) textValue(in string) string {
	if !c.own {
		return in
	}
	if in == "" {
		return ""
	}
	if out, ok := c.text[in]; ok {
		return out
	}
	if !c.charge(len(in)) {
		return ""
	}
	out := strings.Clone(in)
	c.text[in] = out
	return out
}
func (c *syntaxFastCopier) checkStorage() {
	sort.Slice(c.storage, func(i, j int) bool { return c.storage[i].start < c.storage[j].start })
	for i := 1; i < len(c.storage); i++ {
		if c.storage[i].start < c.storage[i-1].end {
			c.err = fmt.Errorf("overlapping syntax storage requires cold parsing")
			return
		}
	}
}
