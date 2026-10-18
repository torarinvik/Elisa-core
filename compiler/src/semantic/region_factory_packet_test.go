package semantic

import (
	"os"
	"strings"
	"testing"
)

func TestExplicitRegionFactoryPacket(t *testing.T) {
	b, err := os.ReadFile("../../../docs/repros/explicit-region-factory-packet.elisa")
	if err != nil {
		t.Fatal(err)
	}
	base := string(b)
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"same owner", base, false},
		{"short factory owner", strings.Replace(base, "out.push(make_packet[r]())", "region scratch:\n            out.push(make_packet[scratch]())", 1), true},
		{"short backing", strings.Replace(base, "        bytes: mutable darray[u8] @r = []\n        bytes.push(7)\n        return Packet[r]{bytes: bytes}", "        region scratch:\n            bytes: mutable darray[u8] @scratch = []\n            bytes.push(7)\n            return Packet[r]{bytes: bytes}", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "packet.elisa", tc.source, AnalyzeOptions{})
			if !tc.reject && len(r.Errors()) != 0 {
				t.Fatal(r.Errors())
			}
			if tc.reject && len(r.Errors()) == 0 {
				t.Fatal("shorter-lived storage accepted")
			}
			if tc.reject {
				message := strings.Join(r.Errors(), "\n")
				if !strings.Contains(message, "scratch") || !(strings.Contains(message, "longer-lived") || strings.Contains(message, "region dependency")) {
					t.Fatalf("expected lifetime rejection, got: %s", message)
				}
			}
		})
	}
}

func TestExplicitRegionFactoryGenericForwarding(t *testing.T) {
	b, err := os.ReadFile("../../../docs/repros/explicit-region-factory-packet.elisa")
	if err != nil {
		t.Fatal(err)
	}
	source := string(b)
	helper := `def forward[@owner]() -> Packet[owner] @owner:
    can Memory.Allocate, Abort.Panic:
        return make_packet[owner]()

`
	source = strings.Replace(source, "def append_packet", helper+"def append_packet", 1)
	source = strings.Replace(source, "out.push(make_packet[r]())", "out.push(forward[r]())", 1)
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "forward.elisa", source, AnalyzeOptions{})
	if len(r.Errors()) != 0 {
		t.Fatal(r.Errors())
	}
}

func TestExplicitRegionFactoryForwardIndependentOwner(t *testing.T) {
	source := `struct Packet:
    bytes: mutable darray[u8]

def make_packet[@r]() -> Packet @r:
    can Memory.Allocate, Abort.Panic:
        bytes: mutable darray[u8] @r = []
        bytes.push(7)
        return Packet{bytes: bytes}

def forward[@r, @s]() -> Packet @r:
    can Memory.Allocate, Abort.Panic:
        return make_packet[s]()
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "forward_independent.elisa", source, AnalyzeOptions{})
	if len(r.Errors()) == 0 {
		t.Fatal("independent factory owner laundered by forwarding")
	}
	t.Log(r.Errors())
}
