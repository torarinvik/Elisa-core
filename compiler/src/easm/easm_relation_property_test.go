package easm

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const easmRelationPropertyCases = 4000

var easmRelationPropertyRegs = []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11"}

type concreteRegisterFile struct {
	regs          map[string]uint64
	defined       map[string]bool
	stack         []concreteValue
	undefinedRead bool
}

type concreteValue struct {
	value   uint64
	defined bool
}

func TestEASMTransitionRelationProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(0xE451A))
	for i := 0; i < easmRelationPropertyCases; i++ {
		body, inputs := generateEASMRelationBody(rng)
		src := wrapEASMRelationBody(body, inputs)
		concrete := executeEASMRelationBody(body, inputs)

		var abstract machineFactState
		verifyFunctionStateHook = func(path string, fn *Function, state machineFactState) {
			if fn.Name == "relation_prop" {
				abstract = state
			}
		}
		_, issues := Parse(fmt.Sprintf("relation_property_%d.easm", i), src)
		verifyFunctionStateHook = nil

		if concrete.undefinedRead && !containsIssue(issues, "register-read-uninitialized") {
			t.Fatalf("case %d: verifier missed concrete undefined read\ninputs: %s\nbody:\n%s\nissues: %#v", i, formatEASMRelationInputs(inputs), strings.Join(body, "\n"), issues)
		}
		for reg, abstractValue := range abstract.KnownUInt {
			if !concrete.defined[reg] {
				t.Fatalf("case %d: verifier claims %s is known, but concrete register is undefined\ninputs: %s\nabstract: %#v\nbody:\n%s\nissues: %#v", i, reg, formatEASMRelationInputs(inputs), abstract.KnownUInt, strings.Join(body, "\n"), issues)
			}
			if concrete.regs[reg] != abstractValue {
				t.Fatalf("case %d: verifier known value mismatch for %s: abstract=%#x concrete=%#x\ninputs: %s\nabstract: %#v\nbody:\n%s\nissues: %#v", i, reg, abstractValue, concrete.regs[reg], formatEASMRelationInputs(inputs), abstract.KnownUInt, strings.Join(body, "\n"), issues)
			}
		}
	}
}

func formatEASMRelationInputs(inputs map[string]uint64) string {
	var parts []string
	for _, reg := range easmRelationPropertyRegs {
		if value, ok := inputs[reg]; ok {
			parts = append(parts, fmt.Sprintf("%s=%#x", reg, value))
		}
	}
	return strings.Join(parts, ", ")
}

func generateEASMRelationBody(rng *rand.Rand) ([]string, map[string]uint64) {
	inputs := map[string]uint64{}
	for _, reg := range easmRelationPropertyRegs {
		if rng.Intn(3) != 0 {
			inputs[reg] = uint64(rng.Intn(17))
		}
	}
	ops := []string{"movq", "addq", "subq", "andq", "xorq", "incq", "decq", "pushq", "popq", "xchgq"}
	imms := []int{0, 1, 2, 3, 7, 15, 16, 31}
	n := 1 + rng.Intn(24)
	body := make([]string, 0, n+1)
	for len(body) < n {
		op := ops[rng.Intn(len(ops))]
		reg := "%" + easmRelationPropertyRegs[rng.Intn(len(easmRelationPropertyRegs))]
		other := "%" + easmRelationPropertyRegs[rng.Intn(len(easmRelationPropertyRegs))]
		imm := fmt.Sprintf("$%d", imms[rng.Intn(len(imms))])
		switch op {
		case "movq":
			if rng.Intn(2) == 0 {
				body = append(body, fmt.Sprintf("        movq %s, %s", imm, reg))
			} else {
				body = append(body, fmt.Sprintf("        movq %s, %s", other, reg))
			}
		case "addq", "subq", "andq":
			if rng.Intn(2) == 0 {
				body = append(body, fmt.Sprintf("        %s %s, %s", op, imm, reg))
			} else {
				body = append(body, fmt.Sprintf("        %s %s, %s", op, other, reg))
			}
		case "xorq":
			if rng.Intn(4) == 0 {
				body = append(body, fmt.Sprintf("        xorq %s, %s", reg, reg))
			} else if rng.Intn(2) == 0 {
				body = append(body, fmt.Sprintf("        xorq %s, %s", imm, reg))
			} else {
				body = append(body, fmt.Sprintf("        xorq %s, %s", other, reg))
			}
		case "incq", "decq", "pushq", "popq":
			body = append(body, fmt.Sprintf("        %s %s", op, reg))
		case "xchgq":
			body = append(body, fmt.Sprintf("        xchgq %s, %s", reg, other))
		}
	}
	body = append(body, "        ret")
	return body, inputs
}

func wrapEASMRelationBody(body []string, inputs map[string]uint64) string {
	var b strings.Builder
	b.WriteString("module relation_property\n")
	b.WriteString("target x86_64\n")
	b.WriteString("export def relation_prop(")
	first := true
	for _, reg := range easmRelationPropertyRegs {
		if _, ok := inputs[reg]; !ok {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(&b, "%s_in: u64", reg)
	}
	b.WriteString(") -> void abi c:\n")
	if len(inputs) != 0 {
		b.WriteString("    inputs: ")
		first = true
		for _, reg := range easmRelationPropertyRegs {
			if _, ok := inputs[reg]; !ok {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "%s_in = %s", reg, reg)
		}
		b.WriteByte('\n')
	}
	b.WriteString("    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory\n")
	b.WriteString("    stack: synthetic\n")
	b.WriteString("    control: returns\n")
	b.WriteString("    requires: input.unused, x86_64.atomic.rmw\n")
	b.WriteString("    body:\n")
	for _, line := range body {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func executeEASMRelationBody(body []string, inputs map[string]uint64) concreteRegisterFile {
	m := concreteRegisterFile{regs: map[string]uint64{}, defined: map[string]bool{}}
	for reg, value := range inputs {
		m.regs[reg] = value
		m.defined[reg] = true
	}
	for _, text := range body {
		m.exec(strings.TrimSpace(text))
	}
	return m
}

func (m *concreteRegisterFile) exec(text string) {
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] == "ret" {
		return
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	read := func(operand string) concreteValue {
		operand = strings.TrimSpace(operand)
		if value, ok := parseImmediateLiteral(operand); ok {
			return concreteValue{value: value, defined: true}
		}
		reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
		if !m.defined[reg] {
			m.undefinedRead = true
			return concreteValue{}
		}
		return concreteValue{value: m.regs[reg], defined: true}
	}
	write := func(operand string, value concreteValue) {
		reg := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(operand), "%"))
		if reg == "" {
			return
		}
		if value.defined {
			m.regs[reg] = value.value
			m.defined[reg] = true
		} else {
			delete(m.regs, reg)
			delete(m.defined, reg)
		}
	}
	bin := func(f func(uint64, uint64) uint64) {
		if len(parts) != 2 {
			return
		}
		src, dst := read(parts[0]), read(parts[1])
		if src.defined && dst.defined {
			write(parts[1], concreteValue{value: f(dst.value, src.value), defined: true})
		} else {
			write(parts[1], concreteValue{})
		}
	}
	switch op {
	case "mov", "movq":
		if len(parts) == 2 {
			write(parts[1], read(parts[0]))
		}
	case "add", "addq":
		bin(func(a, b uint64) uint64 { return a + b })
	case "sub", "subq":
		if len(parts) == 2 && canonicalEASMRelationOperandReg(parts[0]) == canonicalEASMRelationOperandReg(parts[1]) {
			write(parts[1], concreteValue{defined: true})
		} else {
			bin(func(a, b uint64) uint64 { return a - b })
		}
	case "and", "andq":
		bin(func(a, b uint64) uint64 { return a & b })
	case "xor", "xorq":
		if len(parts) == 2 && canonicalEASMRelationOperandReg(parts[0]) == canonicalEASMRelationOperandReg(parts[1]) {
			write(parts[1], concreteValue{defined: true})
		} else {
			bin(func(a, b uint64) uint64 { return a ^ b })
		}
	case "inc", "incq":
		if len(parts) == 1 {
			v := read(parts[0])
			if v.defined {
				v.value++
			}
			write(parts[0], v)
		}
	case "dec", "decq":
		if len(parts) == 1 {
			v := read(parts[0])
			if v.defined {
				v.value--
			}
			write(parts[0], v)
		}
	case "push", "pushq":
		if len(parts) == 1 {
			m.stack = append(m.stack, read(parts[0]))
		}
	case "pop", "popq":
		if len(parts) == 1 {
			top := concreteValue{}
			if len(m.stack) != 0 {
				top = m.stack[len(m.stack)-1]
				m.stack = m.stack[:len(m.stack)-1]
			} else {
				top = concreteValue{defined: true}
			}
			write(parts[0], top)
		}
	case "xchg", "xchgl", "xchgq":
		if len(parts) == 2 {
			left, right := read(parts[0]), read(parts[1])
			write(parts[0], right)
			write(parts[1], left)
		}
	}
}

func canonicalEASMRelationOperandReg(operand string) string {
	return canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(operand), "%"))
}
