package frontendir

// Registry maintenance, exported for `go run ./src/frontendir/gen`.

import (
	"fmt"
	"sort"
	"strings"
)

// RegenerateRegistry returns the registry text that covers the current AST.
//
// Existing assignments are copied through verbatim — including entries for names
// that no longer exist, which are the tombstones that keep retired IDs retired —
// and anything new is appended with the next free ID. The function never
// renumbers, so running it on an unchanged tree reproduces its input.
func RegenerateRegistry() (string, error) {
	schema, err := deriveSchema(schemaRootTypes)
	if err != nil {
		return "", err
	}
	reg, err := parseRegistry(registrySource)
	if err != nil {
		return "", err
	}
	nextType, nextField := reg.nextIDs()

	typeIDs := map[string]uint32{}
	for name, id := range reg.typeIDs {
		typeIDs[name] = id
	}
	fieldIDs := map[string]map[string]uint32{}
	for owner, fields := range reg.fieldIDs {
		fieldIDs[owner] = map[string]uint32{}
		for name, id := range fields {
			fieldIDs[owner][name] = id
		}
	}

	added := 0
	for i := range schema.Types {
		t := &schema.Types[i]
		if _, ok := typeIDs[t.Name]; !ok {
			typeIDs[t.Name] = nextType
			nextType++
			added++
		}
		if fieldIDs[t.Name] == nil {
			fieldIDs[t.Name] = map[string]uint32{}
		}
		for _, f := range t.Fields {
			if _, ok := fieldIDs[t.Name][f.Name]; ok {
				continue
			}
			id, ok := nextField[t.Name]
			if !ok {
				id = 1
			}
			fieldIDs[t.Name][f.Name] = id
			nextField[t.Name] = id + 1
			added++
		}
	}

	header := registryHeader()
	var b strings.Builder
	b.WriteString(header)

	names := make([]string, 0, len(typeIDs))
	for name := range typeIDs {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return typeIDs[names[i]] < typeIDs[names[j]] })
	for _, name := range names {
		fmt.Fprintf(&b, "\nT %d %s\n", typeIDs[name], name)
		fields := make([]string, 0, len(fieldIDs[name]))
		for field := range fieldIDs[name] {
			fields = append(fields, field)
		}
		sort.Slice(fields, func(i, j int) bool { return fieldIDs[name][fields[i]] < fieldIDs[name][fields[j]] })
		for _, field := range fields {
			fmt.Fprintf(&b, "F %s %d %s\n", name, fieldIDs[name][field], field)
		}
	}
	_ = added
	return b.String(), nil
}

// registryHeader preserves the leading comment block of the committed file so
// regeneration does not strip its own instructions.
func registryHeader() string {
	var b strings.Builder
	for _, line := range strings.Split(registrySource, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// LoadSchema derives the schema and stamps it with the committed IDs. It is the
// entry point every encode and decode goes through.
func LoadSchema() (*Schema, error) {
	schema, err := deriveSchema(schemaRootTypes)
	if err != nil {
		return nil, err
	}
	reg, err := parseRegistry(registrySource)
	if err != nil {
		return nil, err
	}
	if err := applyRegistry(schema, reg); err != nil {
		return nil, err
	}
	return schema, nil
}
