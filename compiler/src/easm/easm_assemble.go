package easm

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// TemplateImage is the assembled form of a template (docs/101 §3, Stage 4b): the machine-code
// bytes with each typed hole left zeroed, plus the byte slots the runtime fills at instantiate
// time. The bytes are produced by a real assembler (llvm-mc) — never a hand-rolled encoder — so
// this finally retires the hand-counted ModRM/offset poking in core/linker.elisa.
type TemplateImage struct {
	Bytes   []byte
	Patches []PatchByte
}

// PatchByte is a runtime-filled slot in the assembled bytes.
type PatchByte struct {
	Hole   string
	Offset int
	Width  int
}

var encodingLineRE = regexp.MustCompile(`encoding:\s*\[([0-9a-fA-Fx, ]*)\]`)

// AssemblerAvailable reports whether the llvm-mc backend can be reached.
func AssemblerAvailable() bool {
	_, err := exec.LookPath("llvm-mc")
	return err == nil
}

// AssembleTemplate assembles tpl into bytes + patch-points. Hole offsets are located by
// assembling the body with the hole zeroed vs. all-ones and diffing the byte streams — so the
// offsets are derived from the trusted assembler's encoding, not counted by hand.
func AssembleTemplate(tpl *Template, triple string) (*TemplateImage, error) {
	if tpl == nil {
		return nil, fmt.Errorf("nil template")
	}
	if !AssemblerAvailable() {
		return nil, fmt.Errorf("llvm-mc not available")
	}
	if strings.TrimSpace(triple) == "" {
		triple = "x86_64-apple-darwin"
	}
	base, err := assembleTemplateBody(tpl, holeFills(tpl, ""), triple)
	if err != nil {
		return nil, err
	}
	var patches []PatchByte
	for _, h := range tpl.Holes {
		flipped, err := assembleTemplateBody(tpl, holeFills(tpl, h.Name), triple)
		if err != nil {
			return nil, err
		}
		if len(flipped) != len(base) {
			return nil, fmt.Errorf("hole %s changes the encoding length (%d vs %d)", h.Name, len(flipped), len(base))
		}
		runs := diffRuns(base, flipped)
		if len(runs) == 0 {
			return nil, fmt.Errorf("hole %s is never encoded into a byte slot", h.Name)
		}
		for _, r := range runs {
			patches = append(patches, PatchByte{Hole: h.Name, Offset: r[0], Width: r[1]})
		}
	}
	return &TemplateImage{Bytes: base, Patches: patches}, nil
}

// holeFills maps each hole to an immediate string: the named hole gets all-ones (to expose its
// byte slot under diffing), every other hole gets zero.
func holeFills(tpl *Template, ones string) map[string]string {
	fills := map[string]string{}
	for _, h := range tpl.Holes {
		fills[h.Name] = immediateFor(holeClass(h.Type), h.Name == ones)
	}
	return fills
}

func immediateFor(class string, ones bool) string {
	if !ones {
		return "$0x0"
	}
	if class == "sel16" {
		return "$0xffff"
	}
	return "$0xffffffffffffffff"
}

func assembleTemplateBody(tpl *Template, fills map[string]string, triple string) ([]byte, error) {
	var lines []string
	for _, inst := range tpl.Instructions {
		if inst.Pseudo {
			continue
		}
		lines = append(lines, substituteParams(inst.Text, fills))
	}
	cmd := exec.Command("llvm-mc", "--assemble", "--show-encoding", "--triple="+triple)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("llvm-mc failed: %v\n%s", err, out)
	}
	return parseEncoding(string(out))
}

func parseEncoding(out string) ([]byte, error) {
	var bytesOut []byte
	for _, m := range encodingLineRE.FindAllStringSubmatch(out, -1) {
		for _, tok := range strings.Split(m[1], ",") {
			if tok = strings.TrimSpace(tok); tok == "" {
				continue
			}
			v, err := strconv.ParseUint(strings.TrimPrefix(tok, "0x"), 16, 8)
			if err != nil {
				return nil, fmt.Errorf("unparseable encoding byte %q", tok)
			}
			bytesOut = append(bytesOut, byte(v))
		}
	}
	return bytesOut, nil
}

// InstantiateImage fills the template's holes with concrete values and returns the final bytes —
// the runtime semantics of `template.instantiate(...)`. Immediates are little-endian (x86). This
// is the second half of the byte pipeline: AssembleTemplate produces the holed image at build
// time, InstantiateImage fills it at run time. TestInstantiateMatchesDirectAssembly proves the
// pair is byte-identical to assembling the body with those values directly.
func InstantiateImage(img *TemplateImage, values map[string]uint64) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}
	out := append([]byte(nil), img.Bytes...)
	for _, p := range img.Patches {
		v, ok := values[p.Hole]
		if !ok {
			return nil, fmt.Errorf("no value supplied for hole %s", p.Hole)
		}
		for i := 0; i < p.Width; i++ {
			out[p.Offset+i] = byte(v >> (8 * uint(i)))
		}
	}
	return out, nil
}

// diffRuns returns the contiguous [offset, width] ranges where a and b differ.
func diffRuns(a, b []byte) [][2]int {
	var runs [][2]int
	for i := 0; i < len(a); {
		if a[i] != b[i] {
			start := i
			for i < len(a) && a[i] != b[i] {
				i++
			}
			runs = append(runs, [2]int{start, i - start})
			continue
		}
		i++
	}
	return runs
}
