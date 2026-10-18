package main

import (
	"strings"
	"testing"
)

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

func TestExplicitRegionOnlyFactoryRuns(t *testing.T) {
	source := `struct Packet:
    bytes: mutable darray[u8]

def make[@r]() -> Packet @r:
    can Memory.Allocate, Abort.Panic:
        bytes: mutable darray[u8] @r = []
        bytes.push(7)
        return Packet{bytes: bytes}

def main() -> int:
    can Memory.Allocate, Abort.Panic, Console.Write, Console.Format, Global.Read, Global.Write:
        region owner:
            packet: Packet = make[owner]()
            print(packet.bytes[0].i64())
            return 0
`
	for _, two := range []bool{false, true} {
		current, want := source, "7"
		if two {
			current = strings.Replace(current, "            return 0", "        region other:\n            second: Packet = make[other]()\n            print(second.bytes[0].i64())\n            return 0", 1)
			want = "7\n7"
		}
		status, out := s4CompileRun(t, current)
		if status != "RAN" || out != want {
			t.Fatalf("region-only factory (two owners=%v) failed: %s %q", two, status, out)
		}
	}
}

func TestExplicitTypeAndLifetimeFactoryDistinctOwnersRuns(t *testing.T) {
	status, out := s4CompileRun(t, `def make[T, @r](value: T) -> darray[T] @r:
    can Memory.Allocate, Abort.Panic:
        values: mutable darray[T] @r = []
        values.push(value)
        return values

def main() -> int:
    can Memory.Allocate, Abort.Panic, Console.Write, Console.Format, Global.Read, Global.Write:
        region first:
            values: darray[i64] @first = make[i64, first](7)
            print(values[0])
        region second:
            values: darray[i64] @second = make[i64, second](9)
            print(values[0])
        return 0
`)
	if status != "RAN" || out != "7\n9" {
		t.Fatalf("mixed type/lifetime factory reused wrong owner: %s %q", status, out)
	}
}
