package main

import "testing"

func TestExplicitBufferRegionIndependentOfStackDescriptorRuns(t *testing.T) {
	status, out := s4CompileRun(t, `struct Packet[@storage]:
    bytes: mutable darray[u8] @storage

def append[@storage](packet: mutable Packet[storage]&) -> void:
    can Memory.Allocate, Abort.Panic:
        packet.bytes.push(7)

def main() -> int:
    can Memory.Allocate, Abort.Panic, Console.Write, Console.Format, Global.Read, Global.Write:
        region output:
            bytes: mutable darray[u8] @output = []
            packet: mutable Packet[output] = Packet[output]{bytes: bytes}
            append[output](&packet)
            print(packet.bytes[0].i64())
            return 0
`)
	if status != "RAN" || out != "7" {
		t.Fatalf("explicit buffer owner lost across stack descriptor borrow: %s %q", status, out)
	}
}
