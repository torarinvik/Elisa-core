package frontendir

// Bundle-level encode and decode: header, type table, node table, root.

import (
	"bytes"
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// encodeBundle writes a bundle in the v2 format.
func encodeBundle(schema *Schema, bundle *Bundle) ([]byte, error) {
	e := &encoder{schema: schema, ids: map[nodeKey]uint32{}, used: map[uint32]bool{}}
	rootID := e.nodeID(reflect.ValueOf(bundle.File))
	if e.err != nil {
		return nil, e.err
	}

	// Encoding a node discovers more nodes, so the table is walked as a worklist
	// rather than a fixed range.
	var bodies [][]byte
	for i := 0; i < len(e.nodes); i++ {
		rv := e.nodes[i]
		elem := rv.Elem()
		ts, ok := schema.typeForGo(elem.Type())
		if !ok {
			return nil, fmt.Errorf("no schema for node type %s (run `go run ./src/frontendir/gen`)", elem.Type())
		}
		var body writer
		body.uvarint(uint64(ts.ID))
		e.used[ts.ID] = true
		e.encodeFields(&body, ts, elem)
		if e.err != nil {
			return nil, e.err
		}
		bodies = append(bodies, body.buf.Bytes())
	}

	var w writer
	w.buf.Write(Magic)
	w.uvarint(uint64(SchemaVersion))
	w.str(bundle.SourceFilename)
	w.bytes(bundle.ResolvedSource)
	writeTypeTable(&w, schema, e.used)
	w.uvarint(uint64(len(bodies)))
	for _, body := range bodies {
		w.bytes(body)
	}
	w.uvarint(uint64(rootID))
	return w.buf.Bytes(), nil
}

// rawNode is a node as it appears in the file: a type ID and its fields, still
// undecoded. Holding them undecoded is what allows the two-pass fill — every node
// is allocated before any reference is resolved, so forward and cyclic references
// need no fixups.
type rawNode struct {
	typeID uint32
	fields []rawField
}

type rawField struct {
	id   uint32
	data []byte
}

func readNodeBody(data []byte) (rawNode, error) {
	r := &reader{data: data}
	var node rawNode
	id, err := r.uvarint()
	if err != nil {
		return node, err
	}
	node.typeID = uint32(id)
	count, err := r.uvarint()
	if err != nil {
		return node, err
	}
	for i := uint64(0); i < count; i++ {
		fid, err := r.uvarint()
		if err != nil {
			return node, err
		}
		length, err := r.uvarint()
		if err != nil {
			return node, err
		}
		payload, err := r.take(length)
		if err != nil {
			return node, err
		}
		node.fields = append(node.fields, rawField{id: uint32(fid), data: payload})
	}
	return node, nil
}

// decodeBundle reads a v2 bundle.
//
// The file's own type table is the authority on framing; the local schema is the
// authority on where a value lands. They are reconciled BY NAME, so a file
// written by another implementation — or by a build with a different registry —
// stays readable for every type and field the two share, and anything unknown is
// skipped rather than fatal.
func decodeBundle(schema *Schema, data []byte) (*Bundle, error) {
	r := &reader{data: data}
	magic, err := r.take(uint64(len(Magic)))
	if err != nil || !bytes.Equal(magic, Magic) {
		return nil, fmt.Errorf("not a frontend IR bundle (bad magic)")
	}
	version, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	if version != SchemaVersion {
		return nil, fmt.Errorf("unsupported frontend IR version %d (this build writes %d)", version, SchemaVersion)
	}
	bundle := &Bundle{Version: int(version)}
	if bundle.SourceFilename, err = r.str(); err != nil {
		return nil, err
	}
	sourceLen, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	source, err := r.take(sourceLen)
	if err != nil {
		return nil, err
	}
	bundle.ResolvedSource = append([]byte(nil), source...)

	fileTypes, err := readTypeTable(r)
	if err != nil {
		return nil, err
	}
	d := &decoder{schema: schema, fileTypes: map[uint32]*TypeSchema{}}
	for i := range fileTypes {
		d.fileTypes[fileTypes[i].ID] = &fileTypes[i]
	}

	nodeCount, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	raw := make([]rawNode, 0, nodeCount)
	for i := uint64(0); i < nodeCount; i++ {
		length, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		body, err := r.take(length)
		if err != nil {
			return nil, err
		}
		node, err := readNodeBody(body)
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", i+1, err)
		}
		raw = append(raw, node)
	}
	rootID, err := r.uvarint()
	if err != nil {
		return nil, err
	}

	// Pass one: allocate every node, so references can be resolved without fixups.
	d.nodes = make([]reflect.Value, len(raw)+1)
	for i, node := range raw {
		fileType, ok := d.fileTypes[node.typeID]
		if !ok {
			return nil, fmt.Errorf("node %d references unknown type ID %d", i+1, node.typeID)
		}
		local, ok := schema.TypeByName(fileType.Name)
		if !ok {
			return nil, fmt.Errorf("node %d has type %q, which this compiler does not know", i+1, fileType.Name)
		}
		d.nodes[i+1] = reflect.New(local.goType)
	}

	// Pass two: fill them in.
	for i, node := range raw {
		fileType := d.fileTypes[node.typeID]
		local, _ := schema.TypeByName(fileType.Name)
		if err := d.fillFields(local, fileType, node.fields, d.nodes[i+1].Elem()); err != nil {
			return nil, fmt.Errorf("node %d (%s): %w", i+1, fileType.Name, err)
		}
	}

	if rootID == 0 || rootID >= uint64(len(d.nodes)) {
		return nil, fmt.Errorf("frontend IR bundle has no root AST")
	}
	file, ok := d.nodes[rootID].Interface().(*ast.File)
	if !ok {
		return nil, fmt.Errorf("frontend IR root is %s, want *ast.File", d.nodes[rootID].Type())
	}
	bundle.File = file
	return bundle, nil
}
