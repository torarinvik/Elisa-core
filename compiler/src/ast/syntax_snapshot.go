package ast

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	c := newSyntaxCopier(true, limit)
	c.charge(int(reflect.TypeOf(SyntaxSnapshot{}).Size()))
	value := c.copy(reflect.ValueOf(file), 0)
	if c.err == nil {
		c.checkStorage()
	}
	runtime.KeepAlive(file)
	if c.err != nil {
		return nil, c.err
	}
	return &SyntaxSnapshot{file: value.Interface().(*File), bytes: c.bytes}, nil
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

type syntaxObject struct {
	typ              reflect.Type
	pointer          uintptr
	length, capacity int
}
type syntaxStorage struct{ start, end uintptr }
type syntaxCopier struct {
	own                 bool
	limit, bytes, nodes int
	err                 error
	objects             map[syntaxObject]reflect.Value
	text                map[string]string
	storage             []syntaxStorage
}

func newSyntaxCopier(own bool, limit int) *syntaxCopier {
	return &syntaxCopier{own: own, limit: limit, objects: map[syntaxObject]reflect.Value{}, text: map[string]string{}}
}
func (c *syntaxCopier) charge(size int) bool {
	if size < 0 || size > c.limit-c.bytes {
		c.err = fmt.Errorf("syntax snapshot byte budget exceeded")
		return false
	}
	c.bytes += size
	return true
}
func (c *syntaxCopier) textValue(value string) string {
	if !c.own {
		return value
	}
	if value == "" {
		return ""
	}
	if owned, ok := c.text[value]; ok {
		return owned
	}
	if !c.charge(len(value)) {
		return ""
	}
	owned := strings.Clone(value)
	c.text[value] = owned
	return owned
}
func (c *syntaxCopier) object(value reflect.Value) (syntaxObject, bool) {
	key := syntaxObject{typ: value.Type()}
	switch value.Kind() {
	case reflect.Pointer:
		key.pointer = value.Pointer()
	case reflect.Slice:
		key.pointer = value.Pointer()
		key.length = value.Len()
		key.capacity = value.Cap()
	case reflect.Map:
		key.pointer = uintptr(value.UnsafePointer())
	default:
		return key, false
	}
	return key, true
}
func (c *syntaxCopier) copy(value reflect.Value, depth int) reflect.Value {
	if c.err != nil {
		return reflect.Zero(value.Type())
	}
	if depth > 2048 || c.nodes > 1_000_000 {
		c.err = fmt.Errorf("syntax snapshot work/depth budget exceeded")
		return reflect.Zero(value.Type())
	}
	c.nodes++
	typ := value.Type()
	switch value.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		if value.IsNil() {
			return value
		}
		key, _ := c.object(value)
		if prior, ok := c.objects[key]; ok {
			return prior
		}
		switch value.Kind() {
		case reflect.Pointer:
			if typ.Elem().Size() == 0 {
				c.err = fmt.Errorf("zero-sized syntax identity requires cold parsing")
				return reflect.Zero(typ)
			}
			if !c.charge(int(typ.Elem().Size())) {
				return reflect.Zero(typ)
			}
			out := reflect.New(typ.Elem())
			c.objects[key] = out
			if c.own && typ.Elem().Size() != 0 {
				c.storage = append(c.storage, syntaxStorage{key.pointer, key.pointer + typ.Elem().Size()})
			}
			c.populate(out.Elem(), value.Elem(), depth+1)
			return out
		case reflect.Slice:
			element := int(typ.Elem().Size())
			if element == 0 && value.Cap() > 0 {
				c.err = fmt.Errorf("zero-sized syntax storage requires cold parsing")
				return reflect.Zero(typ)
			}
			if element > 0 && value.Cap() > (c.limit-c.bytes)/element {
				c.err = fmt.Errorf("syntax slice budget exceeded")
				return reflect.Zero(typ)
			}
			if !c.charge(value.Cap() * element) {
				return reflect.Zero(typ)
			}
			out := reflect.MakeSlice(typ, value.Len(), value.Cap())
			c.objects[key] = out
			if c.own && value.Cap()*element > 0 {
				c.storage = append(c.storage, syntaxStorage{key.pointer, key.pointer + uintptr(value.Cap()*element)})
			}
			full := value.Slice(0, value.Cap())
			target := out.Slice(0, out.Cap())
			reflect.Copy(target, full)
			if !syntaxNeedsCopy(typ.Elem(), c.own) {
				return out
			}
			for i := 0; i < full.Len(); i++ {
				if !c.own && i >= value.Len() && full.Index(i).IsZero() {
					continue
				}
				c.populate(target.Index(i), full.Index(i), depth+1)
			}
			return out
		case reflect.Map:
			item := int(typ.Key().Size()+typ.Elem().Size()) * 2
			if !c.charge(128) {
				return reflect.Zero(typ)
			}
			if item > 0 && value.Len() > (c.limit-c.bytes)/item {
				c.err = fmt.Errorf("syntax map budget exceeded")
				return reflect.Zero(typ)
			}
			if !c.charge(value.Len() * item) {
				return reflect.Zero(typ)
			}
			out := reflect.MakeMapWithSize(typ, value.Len())
			c.objects[key] = out
			iter := value.MapRange()
			for iter.Next() {
				out.SetMapIndex(c.copy(iter.Key(), depth+1), c.copy(iter.Value(), depth+1))
			}
			return out
		}
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		out := reflect.New(typ).Elem()
		out.Set(c.copy(value.Elem(), depth+1))
		return out
	case reflect.Struct:
		if typ.PkgPath() != "elisacore/src/ast" && typ.PkgPath() != "elisacore/src/lexer" {
			c.err = fmt.Errorf("non-syntax struct %s", typ)
			return reflect.Zero(typ)
		}
		plan := syntaxCopyPlan(typ, c.own)
		if plan.private {
			c.err = fmt.Errorf("unpublished syntax field in %s", typ)
			return reflect.Zero(typ)
		}
		out := reflect.New(typ).Elem()
		out.Set(value)
		for _, i := range plan.fields {
			out.Field(i).Set(c.copy(value.Field(i), depth+1))
		}
		return out
	case reflect.Array:
		out := reflect.New(typ).Elem()
		out.Set(value)
		if syntaxNeedsCopy(typ.Elem(), c.own) {
			for i := 0; i < value.Len(); i++ {
				out.Index(i).Set(c.copy(value.Index(i), depth+1))
			}
		}
		return out
	case reflect.String:
		if !c.own {
			return value
		}
		out := reflect.New(typ).Elem()
		out.SetString(c.textValue(value.String()))
		return out
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return value
	default:
		c.err = fmt.Errorf("unsupported syntax value %s", typ)
	}
	return reflect.Zero(typ)
}
func (c *syntaxCopier) checkStorage() {
	sort.Slice(c.storage, func(i, j int) bool { return c.storage[i].start < c.storage[j].start })
	for i := 1; i < len(c.storage); i++ {
		if c.storage[i].start < c.storage[i-1].end {
			c.err = fmt.Errorf("overlapping syntax storage requires cold parsing")
			return
		}
	}
}

type syntaxPlanKey struct {
	typ reflect.Type
	own bool
}
type syntaxPlan struct {
	fields  []int
	private bool
}

var syntaxPlans sync.Map

func syntaxCopyPlan(typ reflect.Type, own bool) syntaxPlan {
	key := syntaxPlanKey{typ, own}
	if value, ok := syntaxPlans.Load(key); ok {
		return value.(syntaxPlan)
	}
	plan := syntaxPlan{}
	if _, known := syntaxFastTypes[typ]; !known {
		plan.private = true
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			plan.private = true
		}
		if syntaxNeedsCopy(field.Type, own) {
			plan.fields = append(plan.fields, i)
		}
	}
	value, _ := syntaxPlans.LoadOrStore(key, plan)
	return value.(syntaxPlan)
}
func syntaxNeedsCopy(typ reflect.Type, own bool) bool {
	switch typ.Kind() {
	case reflect.String:
		return own
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return syntaxNeedsCopy(typ.Elem(), own)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).PkgPath != "" || syntaxNeedsCopy(typ.Field(i).Type, own) {
				return true
			}
		}
	}
	return false
}

func (c *syntaxCopier) populate(out, value reflect.Value, depth int) {
	if c.err != nil {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		c.nodes++
		if depth > 2048 || c.nodes > 1_000_000 {
			c.err = fmt.Errorf("syntax snapshot work/depth budget exceeded")
			return
		}
		typ := value.Type()
		if typ.PkgPath() != "elisacore/src/ast" && typ.PkgPath() != "elisacore/src/lexer" {
			c.err = fmt.Errorf("non-syntax struct %s", typ)
			return
		}
		plan := syntaxCopyPlan(typ, c.own)
		if plan.private {
			c.err = fmt.Errorf("unpublished syntax field in %s", typ)
			return
		}
		out.Set(value)
		for _, i := range plan.fields {
			c.populate(out.Field(i), value.Field(i), depth+1)
		}
	case reflect.String:
		if c.own {
			out.SetString(c.textValue(value.String()))
		} else {
			out.Set(value)
		}
	case reflect.Array:
		out.Set(value)
		if syntaxNeedsCopy(value.Type().Elem(), c.own) {
			for i := 0; i < value.Len(); i++ {
				c.populate(out.Index(i), value.Index(i), depth+1)
			}
		}
	default:
		out.Set(c.copy(value, depth))
	}
}
