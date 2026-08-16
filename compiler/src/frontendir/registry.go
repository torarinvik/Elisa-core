package frontendir

// The ID registry: the committed, append-only assignment of numeric IDs to schema
// type and field names.
//
// Deriving the STRUCTURE by reflection keeps the schema honest — it cannot
// describe a field the compiler does not have. But deriving the NUMBERING that
// way would put the wire format back at the mercy of Go declaration order: insert
// a field, and every ID after it shifts, silently invalidating every `.elisair`
// on disk. So the numbering lives in schema_registry.txt, next to this file, and
// is embedded into the binary.
//
// The rules are the usual ones for a wire schema, and TestSchemaMatchesRegistry
// enforces them:
//
//   - An ID, once assigned, is never reused for a different name.
//   - Removing a type or field leaves its entry in place as a tombstone, so the
//     ID stays retired.
//   - A new type or field takes the next free ID, appended at the end.
//
// Renaming a Go field is therefore not a transparent refactor: it shows up as a
// registry change in review, which is the intent.

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed schema_registry.txt
var registrySource string

type registry struct {
	typeIDs  map[string]uint32
	fieldIDs map[string]map[string]uint32
	maxType  uint32
	maxField map[string]uint32
}

// parseRegistry reads the committed ID assignment.
//
//	T <id> <TypeName>
//	F <TypeName> <id> <FieldName>
//
// Blank lines and `#` comments are ignored.
func parseRegistry(text string) (*registry, error) {
	reg := &registry{
		typeIDs:  map[string]uint32{},
		fieldIDs: map[string]map[string]uint32{},
		maxField: map[string]uint32{},
	}
	seenType := map[uint32]string{}
	for lineNo, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		where := fmt.Sprintf("schema_registry.txt:%d", lineNo+1)
		switch fields[0] {
		case "T":
			if len(fields) != 3 {
				return nil, fmt.Errorf("%s: want `T <id> <TypeName>`", where)
			}
			id, err := strconv.ParseUint(fields[1], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", where, err)
			}
			name := fields[2]
			if prev, ok := seenType[uint32(id)]; ok {
				return nil, fmt.Errorf("%s: type ID %d already assigned to %q", where, id, prev)
			}
			if _, ok := reg.typeIDs[name]; ok {
				return nil, fmt.Errorf("%s: type %q assigned twice", where, name)
			}
			seenType[uint32(id)] = name
			reg.typeIDs[name] = uint32(id)
			if uint32(id) > reg.maxType {
				reg.maxType = uint32(id)
			}
		case "F":
			if len(fields) != 4 {
				return nil, fmt.Errorf("%s: want `F <TypeName> <id> <FieldName>`", where)
			}
			owner := fields[1]
			id, err := strconv.ParseUint(fields[2], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", where, err)
			}
			name := fields[3]
			if reg.fieldIDs[owner] == nil {
				reg.fieldIDs[owner] = map[string]uint32{}
			}
			for existing, existingID := range reg.fieldIDs[owner] {
				if existingID == uint32(id) && existing != name {
					return nil, fmt.Errorf("%s: field ID %d on %s already assigned to %q", where, id, owner, existing)
				}
			}
			if _, ok := reg.fieldIDs[owner][name]; ok {
				return nil, fmt.Errorf("%s: field %s.%s assigned twice", where, owner, name)
			}
			reg.fieldIDs[owner][name] = uint32(id)
			if uint32(id) > reg.maxField[owner] {
				reg.maxField[owner] = uint32(id)
			}
		default:
			return nil, fmt.Errorf("%s: unknown record %q", where, fields[0])
		}
	}
	return reg, nil
}

// applyRegistry stamps the committed IDs onto a derived schema. A type or field
// with no entry is reported rather than auto-numbered: assigning an ID is a
// deliberate act, and `go run ./src/frontendir/gen` is what performs it.
func applyRegistry(schema *Schema, reg *registry) error {
	var missing []string
	for i := range schema.Types {
		t := &schema.Types[i]
		id, ok := reg.typeIDs[t.Name]
		if !ok {
			missing = append(missing, "type "+t.Name)
			continue
		}
		t.ID = id
		for j := range t.Fields {
			f := &t.Fields[j]
			fid, ok := reg.fieldIDs[t.Name][f.Name]
			if !ok {
				missing = append(missing, "field "+t.Name+"."+f.Name)
				continue
			}
			f.ID = fid
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		shown := missing
		if len(shown) > 8 {
			shown = shown[:8]
		}
		return fmt.Errorf("frontend IR schema has %d unregistered name(s) (run `go run ./src/frontendir/gen`): %s",
			len(missing), strings.Join(shown, ", "))
	}
	for i := range schema.Types {
		schema.byID[schema.Types[i].ID] = &schema.Types[i]
	}
	return nil
}

// nextIDs reports the next free type ID and, per type, the next free field ID.
// The generator uses these so new names are appended rather than renumbered.
func (r *registry) nextIDs() (uint32, map[string]uint32) {
	next := map[string]uint32{}
	for owner, max := range r.maxField {
		next[owner] = max + 1
	}
	return r.maxType + 1, next
}
