package frontendir

// Decoding values back into the live Go AST.

import (
	"fmt"
	"reflect"
)

type decoder struct {
	schema    *Schema
	fileTypes map[uint32]*TypeSchema
	nodes     []reflect.Value // 1-based, matching node IDs
}

// fillFields populates a struct value from a node's raw fields.
//
// A field the local schema does not have is SKIPPED, not an error: that is the
// forward compatibility the length-delimited framing buys, and it is what lets a
// reader with a smaller AST work through a file it only partly models.
func (d *decoder) fillFields(local *TypeSchema, fileType *TypeSchema, fields []rawField, dst reflect.Value) error {
	for _, raw := range fields {
		fileField, ok := fileType.Field(raw.id)
		if !ok {
			continue // unknown to the file's own schema: unreadable, so skipped
		}
		var target *FieldSchema
		for i := range local.Fields {
			if local.Fields[i].Name == fileField.Name {
				target = &local.Fields[i]
				break
			}
		}
		if target == nil {
			continue // this build has no such field
		}
		if target.Kind != fileField.Kind {
			return fmt.Errorf("field %s: file says %s, this compiler expects %s",
				fileField.Name, fileField.Kind, target.Kind)
		}
		r := &reader{data: raw.data}
		value, err := d.decodeValue(r, &ValueSchema{Kind: fileField.Kind, Elem: fileField.Elem, Key: fileField.Key}, dst.Field(target.goIndex).Type())
		if err != nil {
			return fmt.Errorf("field %s: %w", fileField.Name, err)
		}
		dst.Field(target.goIndex).Set(value)
	}
	return nil
}

// decodeValue reads one value framed by vs and materialises it as goType.
func (d *decoder) decodeValue(r *reader, vs *ValueSchema, goType reflect.Type) (reflect.Value, error) {
	switch vs.Kind {
	case WireBool:
		b, err := r.byteVal()
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(goType).Elem()
		out.SetBool(b != 0)
		return out, nil

	case WireInt:
		v, err := r.varint()
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(goType).Elem()
		switch goType.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			out.SetUint(uint64(v))
		default:
			out.SetInt(v)
		}
		return out, nil

	case WireString:
		s, err := r.str()
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(goType).Elem()
		out.SetString(s)
		return out, nil

	case WireNode:
		id, err := r.uvarint()
		if err != nil {
			return reflect.Value{}, err
		}
		return d.nodeValue(id, goType)

	case WireList:
		count, err := r.uvarint()
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeSlice(goType, 0, int(count))
		for i := uint64(0); i < count; i++ {
			elem, err := d.decodeValue(r, vs.Elem, goType.Elem())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			out = reflect.Append(out, elem)
		}
		return out, nil

	case WireMap:
		count, err := r.uvarint()
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeMapWithSize(goType, int(count))
		for i := uint64(0); i < count; i++ {
			key, err := d.decodeValue(r, vs.Key, goType.Key())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("key %d: %w", i, err)
			}
			value, err := d.decodeValue(r, vs.Elem, goType.Elem())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("value %d: %w", i, err)
			}
			out.SetMapIndex(key, value)
		}
		return out, nil

	case WireStruct:
		return d.decodeInlineStruct(r, goType)

	default:
		return reflect.Value{}, fmt.Errorf("unknown wire kind %d", uint8(vs.Kind))
	}
}

// nodeValue resolves a node reference to the allocated node, as either a concrete
// pointer or an interface holding one.
func (d *decoder) nodeValue(id uint64, goType reflect.Type) (reflect.Value, error) {
	if id == 0 {
		return reflect.Zero(goType), nil
	}
	if id >= uint64(len(d.nodes)) {
		return reflect.Value{}, fmt.Errorf("node reference %d is out of range", id)
	}
	node := d.nodes[id]
	switch goType.Kind() {
	case reflect.Interface:
		if !node.Type().Implements(goType) {
			return reflect.Value{}, fmt.Errorf("%s does not implement %s", node.Type(), goType)
		}
		out := reflect.New(goType).Elem()
		out.Set(node)
		return out, nil
	case reflect.Ptr:
		if node.Type() != goType {
			return reflect.Value{}, fmt.Errorf("node is %s, want %s", node.Type(), goType)
		}
		return node, nil
	default:
		return reflect.Value{}, fmt.Errorf("cannot store a node in %s", goType)
	}
}

// decodeInlineStruct reads a struct written inline (lexer.Pos, ParamDecl, ...).
func (d *decoder) decodeInlineStruct(r *reader, goType reflect.Type) (reflect.Value, error) {
	typeID, err := r.uvarint()
	if err != nil {
		return reflect.Value{}, err
	}
	fileType, ok := d.fileTypes[uint32(typeID)]
	if !ok {
		return reflect.Value{}, fmt.Errorf("inline struct references unknown type ID %d", typeID)
	}
	local, ok := d.schema.TypeByName(fileType.Name)
	if !ok {
		return reflect.Value{}, fmt.Errorf("inline struct type %q is unknown to this compiler", fileType.Name)
	}
	count, err := r.uvarint()
	if err != nil {
		return reflect.Value{}, err
	}
	fields := make([]rawField, 0, count)
	for i := uint64(0); i < count; i++ {
		fid, err := r.uvarint()
		if err != nil {
			return reflect.Value{}, err
		}
		length, err := r.uvarint()
		if err != nil {
			return reflect.Value{}, err
		}
		payload, err := r.take(length)
		if err != nil {
			return reflect.Value{}, err
		}
		fields = append(fields, rawField{id: uint32(fid), data: payload})
	}
	out := reflect.New(goType).Elem()
	if err := d.fillFields(local, fileType, fields, out); err != nil {
		return reflect.Value{}, fmt.Errorf("%s: %w", fileType.Name, err)
	}
	return out, nil
}
