package ast

import (
	"fmt"
	"slices"
	"strings"
	"unsafe"
)

type syntaxStorage struct{ start, end uintptr }

func (c *syntaxFastCopier) captureFieldStorage(in *FieldExpr) bool {
	if c.text != nil || len(c.fieldSlab) == 0 {
		return c.captureStorage(unsafe.Pointer(in), unsafe.Sizeof(*in), 1)
	}
	start := uintptr(unsafe.Pointer(in))
	c.storage = append(c.storage, syntaxStorage{start, start + unsafe.Sizeof(*in)})
	return true
}

func (c *syntaxFastCopier) captureField() *FieldExpr {
	if c.text != nil {
		return new(FieldExpr)
	}
	if len(c.fieldSlab) == 0 {
		size := 4
		if !c.fieldSlabStarted {
			size = 1
			c.fieldSlabStarted = true
		}
		if !c.charge((size - 1) * int(unsafe.Sizeof(FieldExpr{}))) {
			return nil
		}
		c.fieldSlab = make([]FieldExpr, size)
	}
	out := &c.fieldSlab[0]
	c.fieldSlab = c.fieldSlab[1:]
	return out
}

func (c *syntaxFastCopier) captureCallStorage(in *CallExpr) bool {
	if c.text != nil || len(c.callSlab) == 0 {
		return c.captureStorage(unsafe.Pointer(in), unsafe.Sizeof(*in), 1)
	}
	start := uintptr(unsafe.Pointer(in))
	c.storage = append(c.storage, syntaxStorage{start, start + unsafe.Sizeof(*in)})
	return true
}

func (c *syntaxFastCopier) captureCall() *CallExpr {
	if c.text != nil {
		return new(CallExpr)
	}
	if len(c.callSlab) == 0 {
		// Small declarations commonly have just one call. Avoid retaining
		// three unused call nodes in each of their snapshots.
		size := 4
		if !c.callSlabStarted {
			size = 1
			c.callSlabStarted = true
		}
		if !c.charge((size - 1) * int(unsafe.Sizeof(CallExpr{}))) {
			return nil
		}
		c.callSlab = make([]CallExpr, size)
	}
	out := &c.callSlab[0]
	c.callSlab = c.callSlab[1:]
	return out
}

func (c *syntaxFastCopier) captureIdentStorage(in *Ident) bool {
	if c.text != nil || len(c.identSlab) == 0 {
		return c.captureStorage(unsafe.Pointer(in), unsafe.Sizeof(*in), 1)
	}
	// The slab's remaining slots were charged when it was allocated. Still
	// record each source range for the same overlap validation as other nodes.
	start := uintptr(unsafe.Pointer(in))
	c.storage = append(c.storage, syntaxStorage{start, start + unsafe.Sizeof(*in)})
	return true
}

// Transient snapshots have the same lifetime as their owning analysis. Group
// identifiers to reduce per-node allocation without sharing mutable nodes.
func (c *syntaxFastCopier) captureIdent() *Ident {
	if c.text != nil {
		return new(Ident)
	}
	if len(c.identSlab) == 0 {
		// captureStorage already charged the first node. Reserve for the other
		// slots too, so a tight transient budget cannot hide slab capacity.
		// Many functions contain only a few identifiers. Start small to avoid
		// retaining twelve unused nodes in every such snapshot; larger bodies
		// still use sixteen-node slabs after the initial allocation.
		size := 16
		if !c.identSlabStarted {
			size = 4
			c.identSlabStarted = true
		}
		if !c.charge((size - 1) * int(unsafe.Sizeof(Ident{}))) {
			return nil
		}
		c.identSlab = make([]Ident, size)
	}
	out := &c.identSlab[0]
	c.identSlab = c.identSlab[1:]
	return out
}

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
	if !c.own || c.text == nil {
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
	slices.SortFunc(c.storage, func(a, b syntaxStorage) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	for i := 1; i < len(c.storage); i++ {
		if c.storage[i].start < c.storage[i-1].end {
			c.err = fmt.Errorf("overlapping syntax storage requires cold parsing")
			return
		}
	}
}
