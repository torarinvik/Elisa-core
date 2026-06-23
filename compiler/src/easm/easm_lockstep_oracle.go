package easm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const lockstepOracleEnv = "ELISA_EASM_LOCKSTEP_ORACLE"

type oracleBinding struct {
	Name  string
	Type  string
	Reg   string
	IsPtr bool
}

func hasErrorIssue(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

func verifyLockstepOracle(path, target string, fn *Function) []Issue {
	if fn == nil || len(fn.Reference) == 0 || len(fn.Targets) == 0 {
		return nil
	}
	var issues []Issue
	for _, tb := range fn.Targets {
		if strings.ToLower(tb.Arch) != "x86_64" && strings.ToLower(target) != "x86_64" {
			issues = append(issues, lockstepOracleSkip(path, tb.Line, fn.Name, "oracle currently supports x86_64 targets only"))
			continue
		}
		gate, reason := lockstepOracleGate(fn, tb)
		if reason != "" {
			issues = append(issues, lockstepOracleSkip(path, tb.Line, fn.Name, reason))
			continue
		}
		if issue := runLockstepOracle(path, fn, tb, gate); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

func lockstepOracleSkip(path string, line int, name, reason string) Issue {
	return Issue{Severity: "warning", Code: "lockstep-oracle-skip", File: path, Line: line, Message: fmt.Sprintf("lockstep oracle skipped %s: %s", name, reason)}
}

func lockstepOracleGate(fn *Function, tb TargetBody) ([]oracleBinding, string) {
	if len(fn.Targets) == 0 || strings.ToLower(strings.TrimSpace(tb.Lockstep)) != "reference" {
		return nil, "target does not lockstep the reference body"
	}
	if !contractHas(fn.Stack, "unchanged") {
		return nil, "stack contract is not unchanged"
	}
	if !contractHas(fn.Control, "returns") {
		return nil, "control contract is not returns"
	}
	paramTypes := map[string]string{}
	for _, p := range fn.Params {
		paramTypes[strings.ToLower(p.Name)] = p.Type
	}
	var bindings []oracleBinding
	ptrCount := 0
	for _, input := range fn.Inputs {
		name := bindingName(input)
		reg := canonicalX86GPR(registerAfterEquals(input))
		if name == "" || reg == "" || !isX86GPR(reg) {
			return nil, fmt.Sprintf("input binding %q is not a concrete x86_64 GPR", input)
		}
		typ := paramTypes[name]
		b := oracleBinding{Name: name, Type: typ, Reg: reg, IsPtr: strings.HasPrefix(strings.TrimSpace(typ), "HostPtr[")}
		if b.IsPtr {
			ptrCount++
		}
		bindings = append(bindings, b)
	}
	if ptrCount > 1 {
		return nil, "more than one HostPtr input"
	}
	if reason := lockstepOracleGateBody(fn, fn.Reference, bindings); reason != "" {
		return nil, "reference " + reason
	}
	if reason := lockstepOracleGateBody(fn, tb.Instructions, bindings); reason != "" {
		return nil, "target " + reason
	}
	return bindings, ""
}

func lockstepOracleGateBody(fn *Function, insts []Instruction, bindings []oracleBinding) string {
	ptrRegs := map[string]bool{}
	for _, b := range bindings {
		if b.IsPtr {
			ptrRegs[b.Reg] = true
		}
	}
	for _, inst := range insts {
		if inst.Pseudo {
			return fmt.Sprintf("contains pseudo-op %q", inst.Op)
		}
		op := normalizeOp(inst.Op)
		switch op {
		case "call", "callq", "rdtsc", "cpuid", "pause", "lfence", "mrs", "isb", "syscall", "sysenter":
			return fmt.Sprintf("contains non-leaf or ambient-side-effect instruction %q", inst.Op)
		case "jmp", "jmpq", "ja", "jae", "jb", "jbe", "jc", "je", "jg", "jge", "jl", "jle", "jna", "jnae", "jnb", "jnbe", "jnc", "jne", "jng", "jnge", "jnl", "jnle", "jno", "jnp", "jns", "jnz", "jo", "jp", "jpe", "jpo", "js", "jz":
			return fmt.Sprintf("contains non-local control transfer %q", inst.Op)
		}
		lower := strings.ToLower(inst.Text)
		if strings.Contains(lower, "%fs:") || strings.Contains(lower, "%gs:") {
			return "contains segment memory access"
		}
		if stackPointerWritten(inst.Text) {
			return "contains stack switch"
		}
		for _, operand := range splitInstructionOperands(inst.Text) {
			if !operandIsMemory(operand) {
				continue
			}
			base := memoryBaseRegister(operand)
			if base == "" || !ptrRegs[base] {
				return fmt.Sprintf("contains memory operand %q whose base is not the single HostPtr input", operand)
			}
		}
	}
	return ""
}

func runLockstepOracle(path string, fn *Function, tb TargetBody, bindings []oracleBinding) *Issue {
	llvmMC := findToolForLockstepOracle("llvm-mc")
	clang := findToolForLockstepOracle("clang")
	if clang == "" {
		clang = findToolForLockstepOracle("clang++")
	}
	if llvmMC == "" || clang == "" {
		return issuePtr(lockstepOracleSkip(path, tb.Line, fn.Name, "llvm-mc and clang are not available"))
	}
	dir, err := os.MkdirTemp("", "elisa-easm-lockstep-*")
	if err != nil {
		return issuePtr(Issue{Severity: "warning", Code: "lockstep-oracle-skip", File: path, Line: tb.Line, Message: fmt.Sprintf("lockstep oracle skipped %s: %v", fn.Name, err)})
	}
	defer os.RemoveAll(dir)
	refObj, err := assembleOracleObject(llvmMC, dir, "ElisaLockstepReference", fn.Reference)
	if err != nil {
		return issuePtr(lockstepOracleSkip(path, tb.Line, fn.Name, err.Error()))
	}
	targetObj, err := assembleOracleObject(llvmMC, dir, "ElisaLockstepTarget", tb.Instructions)
	if err != nil {
		return issuePtr(lockstepOracleSkip(path, tb.Line, fn.Name, err.Error()))
	}
	cPath := filepath.Join(dir, "oracle.c")
	exePath := filepath.Join(dir, "oracle")
	if err := os.WriteFile(cPath, []byte(oracleCSource(bindings)), 0o644); err != nil {
		return issuePtr(lockstepOracleSkip(path, tb.Line, fn.Name, err.Error()))
	}
	cmd := exec.Command(clang, "-arch", "x86_64", cPath, refObj, targetObj, "-o", exePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return issuePtr(lockstepOracleSkip(path, tb.Line, fn.Name, fmt.Sprintf("could not link oracle probe: %v\n%s", err, out)))
	}
	out, err := exec.Command(exePath).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return issuePtr(Issue{Severity: "error", Code: "lockstep-divergence", File: path, Line: tb.Line, Message: msg})
	}
	return nil
}

func issuePtr(issue Issue) *Issue { return &issue }

func assembleOracleObject(llvmMC, dir, sym string, insts []Instruction) (string, error) {
	var asm bytes.Buffer
	asm.WriteString(".text\n.globl _" + sym + "\n_" + sym + ":\n")
	for _, inst := range insts {
		if inst.Pseudo {
			continue
		}
		asm.WriteString("\t" + inst.Text + "\n")
	}
	key := sha256.Sum256(asm.Bytes())
	sPath := filepath.Join(dir, sym+"_"+hex.EncodeToString(key[:4])+".s")
	oPath := filepath.Join(dir, sym+".o")
	if err := os.WriteFile(sPath, asm.Bytes(), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command(llvmMC, "--assemble", "--filetype=obj", "--triple=x86_64-apple-darwin", "-o", oPath, sPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("llvm-mc could not assemble oracle body: %v\n%s", err, out)
	}
	return oPath, nil
}

func oracleCSource(bindings []oracleBinding) string {
	args := make([]string, len(bindings))
	values := make([]string, len(bindings))
	for i, b := range bindings {
		args[i] = "uint64_t"
		if b.IsPtr {
			values[i] = "(uint64_t)buf"
		} else {
			values[i] = fmt.Sprintf("v[%d]", i)
		}
	}
	callArgs := strings.Join(values, ", ")
	if callArgs == "" {
		callArgs = ""
	}
	vecs := oracleVectors(len(bindings))
	return strings.ReplaceAll(fmt.Sprintf(`#include <stdint.h>
#include <stdio.h>
#include <string.h>
extern uint64_t %[1]s(%[2]s) asm("_%[1]s");
extern uint64_t %[3]s(%[2]s) asm("_%[3]s");
static const uint64_t vectors[][8] = {
%[4]s
};
int main(void) {
  for (unsigned i = 0; i < sizeof(vectors)/sizeof(vectors[0]); i++) {
    uint64_t v[8];
    memcpy(v, vectors[i], sizeof(v));
    unsigned char ref_mem[64], target_mem[64], *buf;
    for (unsigned j = 0; j < sizeof(ref_mem); j++) ref_mem[j] = (unsigned char)(0xa5u ^ (i * 17u + j * 29u));
    memcpy(target_mem, ref_mem, sizeof(ref_mem));
    buf = ref_mem;
    uint64_t rr = %[1]s(%[5]s);
    buf = target_mem;
    uint64_t tr = %[3]s(%[5]s);
    if (rr != tr) {
      fprintf(stderr, "lockstep-divergence vector=@PERCENT@u register=rax reference=0x@PERCENT@llx target=0x@PERCENT@llx\n", i, (unsigned long long)rr, (unsigned long long)tr);
      return 1;
    }
    if (memcmp(ref_mem, target_mem, sizeof(ref_mem)) != 0) {
      for (unsigned j = 0; j < sizeof(ref_mem); j++) if (ref_mem[j] != target_mem[j]) {
        fprintf(stderr, "lockstep-divergence vector=@PERCENT@u memory[@PERCENT@u] reference=0x@PERCENT@02x target=0x@PERCENT@02x\n", i, j, ref_mem[j], target_mem[j]);
        return 1;
      }
    }
  }
  return 0;
}
`, "ElisaLockstepReference", strings.Join(args, ", "), "ElisaLockstepTarget", vecs, callArgs), "@PERCENT@", "%")
}

func oracleVectors(width int) string {
	if width < 1 {
		width = 1
	}
	seeds := []uint64{0, 1, ^uint64(0), 0x7fffffffffffffff, 0x8000000000000000, 0x0123456789abcdef, 0xfedcba9876543210, 0xaaaaaaaa55555555}
	var lines []string
	for i := 0; i < 16; i++ {
		var vals [8]uint64
		for j := 0; j < 8; j++ {
			vals[j] = seeds[(i+j)%len(seeds)] ^ uint64(i+1)*0x9e3779b97f4a7c15 ^ uint64(j)*0xd1b54a32d192ed03
		}
		var parts []string
		for j := 0; j < 8; j++ {
			parts = append(parts, fmt.Sprintf("UINT64_C(0x%016x)", vals[j]))
		}
		lines = append(lines, "  {"+strings.Join(parts, ", ")+"},")
	}
	return strings.Join(lines, "\n")
}

func findToolForLockstepOracle(name string) string {
	envName := "ELISA_EASM_LOCKSTEP_ORACLE_" + strings.ToUpper(strings.NewReplacer("-", "_", "+", "_").Replace(name))
	if override, ok := os.LookupEnv(envName); ok {
		if override == "" {
			return ""
		}
		if fi, err := os.Stat(override); err == nil && !fi.IsDir() {
			return override
		}
		return ""
	}
	for _, prefix := range []string{"/opt/homebrew/opt/llvm/bin", "/usr/local/opt/llvm/bin"} {
		p := filepath.Join(prefix, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func contractHas(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, v := range contractFields(values) {
		if strings.ToLower(strings.TrimSpace(v)) == want {
			return true
		}
	}
	return false
}

func stackPointerWritten(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	op := normalizeOp(strings.Fields(text)[0])
	switch op {
	case "push", "pushq", "pop", "popq", "call", "callq", "ret", "retq":
		return op != "ret" && op != "retq"
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	return canonicalX86GPR(strings.TrimPrefix(dst, "%")) == "rsp"
}
