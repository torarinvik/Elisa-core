package frontendir

// The v2 wire codec.
//
// Layout (all integers uvarint unless noted; ints are zigzagged):
//
//	magic "ELISAIR2\n"
//	version
//	string  source filename
//	bytes   resolved source
//	type table:   count, then per type: id, name, field count,
//	              then per field: id, name, value descriptor
//	node table:   count, then per node: type id, live field count,
//	              then per field: field id, byte length, bytes
//	root node id
//
// Two framing decisions carry the format's guarantees:
//
//   - Every node lives in the table and is referred to by INDEX. Pointer identity,
//     sharing and cycles therefore survive a round-trip exactly — which gob could
//     not do, and which `ast.File.DeclVisibility` (a map keyed by decl identity)
//     depends on.
//   - Every top-level field is length-delimited and the type table is carried in
//     the file. A reader can skip a field or a whole type it does not model and
//     still walk, and re-emit, everything else. That is what lets an
//     implementation with a smaller AST consume these files at all.
//
// Fields at their zero value are omitted; a decoder leaves them zero.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
)

type writer struct {
	buf bytes.Buffer
}

func (w *writer) uvarint(v uint64) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], v)
	w.buf.Write(scratch[:n])
}

func (w *writer) varint(v int64) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutVarint(scratch[:], v)
	w.buf.Write(scratch[:n])
}

func (w *writer) str(s string) {
	w.uvarint(uint64(len(s)))
	w.buf.WriteString(s)
}

func (w *writer) bytes(b []byte) {
	w.uvarint(uint64(len(b)))
	w.buf.Write(b)
}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.data[r.pos:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated uvarint at offset %d", r.pos)
	}
	r.pos += n
	return v, nil
}

func (r *reader) varint() (int64, error) {
	v, n := binary.Varint(r.data[r.pos:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated varint at offset %d", r.pos)
	}
	r.pos += n
	return v, nil
}

func (r *reader) take(n uint64) ([]byte, error) {
	if uint64(len(r.data)-r.pos) < n {
		return nil, fmt.Errorf("truncated: want %d bytes at offset %d, have %d", n, r.pos, len(r.data)-r.pos)
	}
	out := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *reader) str() (string, error) {
	n, err := r.uvarint()
	if err != nil {
		return "", err
	}
	b, err := r.take(n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *reader) byteVal() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// --- type table ------------------------------------------------------------

func writeValueSchema(w *writer, vs *ValueSchema) {
	w.buf.WriteByte(byte(vs.Kind))
	switch vs.Kind {
	case WireList:
		writeValueSchema(w, vs.Elem)
	case WireMap:
		writeValueSchema(w, vs.Key)
		writeValueSchema(w, vs.Elem)
	case WireStruct:
		w.str(vs.StructType)
	}
}

func readValueSchema(r *reader) (*ValueSchema, error) {
	kind, err := r.byteVal()
	if err != nil {
		return nil, err
	}
	vs := &ValueSchema{Kind: WireKind(kind)}
	switch vs.Kind {
	case WireList:
		if vs.Elem, err = readValueSchema(r); err != nil {
			return nil, err
		}
	case WireMap:
		if vs.Key, err = readValueSchema(r); err != nil {
			return nil, err
		}
		if vs.Elem, err = readValueSchema(r); err != nil {
			return nil, err
		}
	case WireStruct:
		if vs.StructType, err = r.str(); err != nil {
			return nil, err
		}
	}
	return vs, nil
}

// writeTypeTable describes the types the bundle contains. A nil `used` set means
// describe everything, which is what the size probe and the schema dump want.
func writeTypeTable(w *writer, schema *Schema, used map[uint32]bool) {
	described := make([]*TypeSchema, 0, len(schema.Types))
	for i := range schema.Types {
		if used == nil || used[schema.Types[i].ID] {
			described = append(described, &schema.Types[i])
		}
	}
	w.uvarint(uint64(len(described)))
	for _, t := range described {
		w.uvarint(uint64(t.ID))
		w.str(t.Name)
		w.uvarint(uint64(len(t.Fields)))
		for j := range t.Fields {
			f := &t.Fields[j]
			w.uvarint(uint64(f.ID))
			w.str(f.Name)
			writeValueSchema(w, &ValueSchema{Kind: f.Kind, Elem: f.Elem, Key: f.Key})
		}
	}
}

// readTypeTable reads the schema carried by the file. It is returned as-is; the
// decoder reconciles it with the schema this build knows, so a file written by a
// different version stays readable for everything the two have in common.
func readTypeTable(r *reader) ([]TypeSchema, error) {
	count, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	types := make([]TypeSchema, 0, count)
	for i := uint64(0); i < count; i++ {
		var t TypeSchema
		id, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		t.ID = uint32(id)
		if t.Name, err = r.str(); err != nil {
			return nil, err
		}
		fieldCount, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		for j := uint64(0); j < fieldCount; j++ {
			var f FieldSchema
			fid, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			f.ID = uint32(fid)
			if f.Name, err = r.str(); err != nil {
				return nil, err
			}
			vs, err := readValueSchema(r)
			if err != nil {
				return nil, err
			}
			f.Kind, f.Elem, f.Key = vs.Kind, vs.Elem, vs.Key
			t.Fields = append(t.Fields, f)
		}
		types = append(types, t)
	}
	return types, nil
}

// --- encoding --------------------------------------------------------------

type nodeKey struct {
	ptr uintptr
	typ reflect.Type
}

type encoder struct {
	schema *Schema
	ids    map[nodeKey]uint32
	nodes  []reflect.Value // index 0 unused; node IDs are 1-based
	// used records the type IDs the file actually contains. Only those are
	// described in the type table: a 50-line program touches ~40 of the AST's 279
	// types, and describing the other 239 would be most of a small bundle.
	used map[uint32]bool
	err  error
}

// nodeID interns a pointer-to-struct value, assigning it a table index. A nil
// reference is 0.
func (e *encoder) nodeID(rv reflect.Value) uint32 {
	for rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Ptr {
		e.failf("expected a pointer node, got %s", rv.Type())
		return 0
	}
	if rv.IsNil() {
		return 0
	}
	key := nodeKey{ptr: rv.Pointer(), typ: rv.Type()}
	if id, ok := e.ids[key]; ok {
		return id
	}
	e.nodes = append(e.nodes, rv)
	id := uint32(len(e.nodes))
	e.ids[key] = id
	return id
}

func (e *encoder) failf(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf(format, args...)
	}
}

// encodeValue writes one value in the framing its kind implies. Node references
// are interned, which may append to the worklist — collectNodes drives that to a
// fixpoint.
func (e *encoder) encodeValue(w *writer, vs *ValueSchema, rv reflect.Value) {
	switch vs.Kind {
	case WireBool:
		if rv.Bool() {
			w.buf.WriteByte(1)
		} else {
			w.buf.WriteByte(0)
		}
	case WireInt:
		switch rv.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			w.varint(int64(rv.Uint()))
		default:
			w.varint(rv.Int())
		}
	case WireString:
		w.str(rv.String())
	case WireNode:
		w.uvarint(uint64(e.nodeID(rv)))
	case WireList:
		n := rv.Len()
		w.uvarint(uint64(n))
		for i := 0; i < n; i++ {
			e.encodeValue(w, vs.Elem, rv.Index(i))
		}
	case WireMap:
		e.encodeMap(w, vs, rv)
	case WireStruct:
		e.encodeStruct(w, rv)
	default:
		e.failf("cannot encode wire kind %s", vs.Kind)
	}
}

// encodeMap writes a map as (key, value) pairs.
//
// Go randomises map iteration, so the pairs are sorted before writing: by key
// text for a string key, and otherwise by the key node's table index, which
// encodeFields has already assigned (see the note there on why maps go last).
// Either way the bytes are a function of the input alone, so a rebuild of an
// unchanged tree is byte-identical.
func (e *encoder) encodeMap(w *writer, vs *ValueSchema, rv reflect.Value) {
	keys := rv.MapKeys()
	type pair struct {
		key   reflect.Value
		order string
		id    uint32
	}
	pairs := make([]pair, 0, len(keys))
	for _, k := range keys {
		p := pair{key: k}
		if vs.Key.Kind == WireString {
			p.order = k.String()
		} else {
			p.id = e.nodeID(k)
		}
		pairs = append(pairs, p)
	}
	sortPairs := func(i, j int) bool {
		if vs.Key.Kind == WireString {
			return pairs[i].order < pairs[j].order
		}
		return pairs[i].id < pairs[j].id
	}
	stableSort(len(pairs), sortPairs, func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	w.uvarint(uint64(len(pairs)))
	for _, p := range pairs {
		if vs.Key.Kind == WireString {
			w.str(p.order)
		} else {
			w.uvarint(uint64(p.id))
		}
		e.encodeValue(w, vs.Elem, rv.MapIndex(p.key))
	}
}

// encodeStruct writes an inline struct value: its type ID, then its live fields,
// each length-delimited so an unknown field can be skipped.
func (e *encoder) encodeStruct(w *writer, rv reflect.Value) {
	ts, ok := e.schema.typeForGo(rv.Type())
	if !ok {
		e.failf("no schema for inline struct %s", rv.Type())
		return
	}
	w.uvarint(uint64(ts.ID))
	e.used[ts.ID] = true
	e.encodeFields(w, ts, rv)
}

// encodeFields writes the non-zero fields of a struct value.
//
// Map fields are written LAST, and that ordering is load-bearing rather than
// cosmetic. A map keyed by a node has to serialise its keys as node references,
// and a reference is only stable if the node already has a table index. Fields
// are otherwise visited in schema (alphabetical) order, which would put
// `DeclVisibility` before `Decls` and force the encoder to intern the map's keys
// in Go's randomised map-iteration order — making the output differ run to run.
// Encoding the plain fields first means every key already has its ID by the time
// the map is reached. Field order on the wire carries no meaning of its own:
// each field is tagged with its ID, so a reader is unaffected.
func (e *encoder) encodeFields(w *writer, ts *TypeSchema, rv reflect.Value) {
	var body writer
	live := 0
	emit := func(f *FieldSchema) {
		fv := rv.Field(f.goIndex)
		if isZeroValue(fv) {
			return
		}
		var payload writer
		e.encodeValue(&payload, &ValueSchema{Kind: f.Kind, Elem: f.Elem, Key: f.Key}, fv)
		body.uvarint(uint64(f.ID))
		body.bytes(payload.buf.Bytes())
		live++
	}
	for i := range ts.Fields {
		if ts.Fields[i].Kind != WireMap {
			emit(&ts.Fields[i])
		}
	}
	for i := range ts.Fields {
		if ts.Fields[i].Kind == WireMap {
			emit(&ts.Fields[i])
		}
	}
	w.uvarint(uint64(live))
	w.buf.Write(body.buf.Bytes())
}

// isZeroValue reports whether a field can be omitted. Empty slices and maps are
// omitted alongside zero scalars; a decoder that leaves them nil is
// indistinguishable to every consumer of the AST.
func isZeroValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return rv.IsNil()
	case reflect.Struct:
		return rv.IsZero()
	default:
		return rv.IsZero()
	}
}

// stableSort is an insertion sort over an index-addressed sequence. The sequences
// here are map key lists — short — and insertion sort keeps the comparator and
// swap explicit without another reflect-shaped adapter.
func stableSort(n int, less func(i, j int) bool, swap func(i, j int)) {
	for i := 1; i < n; i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			swap(j, j-1)
		}
	}
}
