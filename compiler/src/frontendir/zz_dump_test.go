package frontendir

import (
	"fmt"
	"os"
	"testing"
)

func vsDescV(vs *ValueSchema) string {
	if vs.Kind == WireStruct {
		return "7[" + vs.StructType + "]"
	}
	return vsDesc(vs.Kind, vs.Elem, vs.Key)
}

func vsDesc(k WireKind, elem, key *ValueSchema) string {
	switch k {
	case WireList:
		return "5(" + vsDescV(elem) + ")"
	case WireMap:
		return "6(" + vsDescV(key) + "," + vsDescV(elem) + ")"
	}
	return fmt.Sprintf("%d", uint8(k))
}

func TestZZDumpSchema(t *testing.T) {
	if os.Getenv("ZZDUMP") == "" {
		t.Skip("set ZZDUMP")
	}
	s, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(os.Getenv("ZZDUMP"))
	defer f.Close()
	for i := range s.Types {
		ts := &s.Types[i]
		fmt.Fprintf(f, "T %d %s\n", ts.ID, ts.Name)
		for j := range ts.Fields {
			fd := &ts.Fields[j]
			fmt.Fprintf(f, "F %d %s %s\n", fd.ID, fd.Name, vsDesc(fd.Kind, fd.Elem, fd.Key))
		}
	}
}
