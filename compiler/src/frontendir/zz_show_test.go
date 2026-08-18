package frontendir

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestZZShowBundle prints a bundle's node table: type name, then each live field
// with its schema name. Used to READ stage0's own mapping instead of guessing it.
func TestZZShowBundle(t *testing.T) {
	path := os.Getenv("ZZSHOW")
	if path == "" {
		t.Skip("set ZZSHOW")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &reader{data: data}
	if _, err := r.take(uint64(len(Magic))); err != nil {
		t.Fatal(err)
	}
	r.uvarint()
	r.str()
	n, _ := r.uvarint()
	r.take(n)
	fileTypes, err := readTypeTable(r)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uint32]*TypeSchema{}
	for i := range fileTypes {
		byID[fileTypes[i].ID] = &fileTypes[i]
	}
	count, _ := r.uvarint()
	for i := uint64(0); i < count; i++ {
		length, _ := r.uvarint()
		body, _ := r.take(length)
		node, err := readNodeBody(body)
		if err != nil {
			t.Fatal(err)
		}
		ts := byID[node.typeID]
		names := []string{}
		for _, f := range node.fields {
			fs, ok := ts.Field(f.id)
			label := fmt.Sprintf("?%d", f.id)
			if ok {
				label = fs.Name
				switch fs.Kind {
				case WireString:
					rr := &reader{data: f.data}
					s, _ := rr.str()
					label += "=" + s
				case WireNode:
					rr := &reader{data: f.data}
					v, _ := rr.uvarint()
					label += fmt.Sprintf("=#%d", v)
				case WireInt:
					rr := &reader{data: f.data}
					v, _ := rr.varint()
					label += fmt.Sprintf("=%d", v)
				case WireBool:
					label += fmt.Sprintf("=%v", f.data[0] != 0)
				case WireList:
					rr := &reader{data: f.data}
					c, _ := rr.uvarint()
					label += fmt.Sprintf("[%d]", c)
					if fs.Elem != nil && fs.Elem.Kind == WireNode {
						ids := []string{}
						for j := uint64(0); j < c; j++ {
							v, _ := rr.uvarint()
							ids = append(ids, fmt.Sprintf("#%d", v))
						}
						label += fmt.Sprint(ids)
					}
				}
			}
			names = append(names, label)
		}
		sort.Strings(names)
		fmt.Printf("#%d %s %v\n", i+1, ts.Name, names)
	}
	root, _ := r.uvarint()
	fmt.Printf("root=#%d\n", root)
}
