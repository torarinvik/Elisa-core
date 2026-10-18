package semantic

import (
	"strings"
	"testing"
)

func TestAggregateStorageRegionSeparateFromBorrowedSlot(t *testing.T) {
	source := `struct Packet[@storage]:
    bytes: mutable darray[u8] @storage

def append[@storage](packet: mutable Packet[storage]&) -> void:
    can Memory.Allocate, Abort.Panic:
        packet.bytes.push(7)

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        region output:
            bytes: mutable darray[u8] @output = []
            packet: mutable Packet[output] = Packet[output]{bytes: bytes}
            append[output](&packet)
            return packet.bytes[0].i64()
`
	for _, coupled := range []bool{false, true} {
		text := source
		if coupled {
			text = strings.Replace(text, "packet: mutable Packet[storage]&)", "packet: mutable Packet[storage]& @storage)", 1)
		}
		r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "slot.elisa", text, AnalyzeOptions{})
		if !coupled && len(r.Errors()) != 0 {
			t.Fatal(r.Errors())
		}
		if coupled && len(r.Errors()) == 0 {
			t.Fatal("stack slot accepted as output-owned reference")
		}
		if coupled {
			if !strings.Contains(strings.Join(r.Errors(), "\n"), "got stack mutable Packet") {
				t.Fatalf("wrong rejection: %v", r.Errors())
			}
		}
		if !coupled {
			symbol, ok := r.GlobalScope.Lookup("append")
			if !ok {
				t.Fatal("missing append function")
			}
			fn := symbol.Type.(*FuncType)
			if len(fn.InferredRegionParams) != 0 {
				t.Fatalf("unneeded descriptor-slot region: %v", fn.InferredRegionParams)
			}
		}
	}
}
