#!/usr/bin/env python3
"""Qualify the ordered AArch64 signed-16 scan comparison rewrite."""
import ctypes
import os
import random
import shlex
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if os.uname().machine not in ('arm64', 'aarch64'):
    print('SKIP: AArch64 only'); raise SystemExit(0)
TIMEOUT = 'gtimeout' if os.uname().sysname == 'Darwin' else 'timeout'
CONFIG = os.environ.get('LLVM_CONFIG', '/opt/homebrew/opt/llvm@23/bin/llvm-config' if os.uname().sysname == 'Darwin' else '/usr/lib/llvm-23/bin/llvm-config')
TRIPLE = 'arm64-apple-darwin' if os.uname().sysname == 'Darwin' else 'aarch64-unknown-linux-gnu'
def run(args):
    p = subprocess.run([TIMEOUT, '-k', '1s', '28s', *map(str, args)], text=True, capture_output=True)
    if p.returncode: raise RuntimeError(f'{args}: {p.stderr}')
    return p.stdout.strip()
def flags(*args): return shlex.split(run([CONFIG, *args]))
def fixture(count=8, offset=-32768, wrap='', attrs=''):
    lines = [f'target triple = "{TRIPLE}"', f'define i1 @outside(ptr %p, i64 %n) {attrs} {{', 'entry:', '  br label %loop', 'loop:', '  %i = phi i64 [ 0, %entry ], [ %next, %latch ]']
    for j in range(count):
        lines += [f'  %p{j} = getelementptr i64, ptr %p, i64 {j}', f'  %v{j} = load i64, ptr %p{j}, align 8', f'  %a{j} = add {wrap}i64 %v{j}, {offset}', f'  %c{j} = icmp ult i64 %a{j}, -65536']
        if j: lines.append(f'  %o{j} = or i1 %{("c0" if j == 1 else "o"+str(j-1))}, %c{j}')
    lines += [f'  br i1 %{("c0" if count == 1 else "o"+str(count-1))}, label %bad, label %latch', 'latch:', '  %next = add i64 %i, 1', '  %done = icmp uge i64 %next, %n', '  br i1 %done, label %good, label %loop', 'bad:', '  ret i1 true', 'good:', '  ret i1 false', '}']
    return '\n'.join(lines) + '\n'
DRIVER = r'''
#include <llvm-c/Core.h>
#include <llvm/IR/LLVMContext.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Support/raw_ostream.h>
extern "C" unsigned elisacoreCompactEvenRanges(LLVMModuleRef);
int main(int argc,char **argv) { llvm::LLVMContext c; llvm::SMDiagnostic d; auto m=llvm::parseIRFile(argv[1],d,c); if(!m){d.print(argv[0],llvm::errs());return 2;} auto a=elisacoreCompactEvenRanges(llvm::wrap(m.get())); auto b=elisacoreCompactEvenRanges(llvm::wrap(m.get())); if(llvm::verifyModule(*m,&llvm::errs()))return 3; std::error_code e; llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);llvm::outs()<<a<<" "<<b;return e?4:0; }
'''
with tempfile.TemporaryDirectory(prefix='elisa-signed16-scan-') as tmp:
    d=Path(tmp);bindir=Path(run([CONFIG,'--bindir']));libdir=run([CONFIG,'--libdir']);(d/'driver.cpp').write_text(DRIVER)
    run([bindir/'clang++',*flags('--cxxflags'),d/'driver.cpp',ROOT/'src/backend/llvm_wrappers/llvm_even_range.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
    cases={'eligible':(fixture(),'8 0'),'short loop':(fixture(7),'0 0'),'wrong offset':(fixture(offset=-32767),'0 0'),'signed wrap':(fixture(wrap='nsw '),'0 0'),'unsigned wrap':(fixture(wrap='nuw '),'0 0'),'optsize':(fixture(attrs='optsize'),'0 0'),'asan':(fixture(attrs='sanitize_address'),'0 0')}
    for name,(ir,want) in cases.items():
        inp=d/(name.replace(' ','_')+'.ll');out=d/(name.replace(' ','_')+'.out.ll');inp.write_text(ir);got=run([d/'driver',inp,out]);assert got==want,(name,got,want);print('PASS',name,flush=True)
    original=d/'eligible.ll';transformed=d/'eligible.out.ll';funcs=[]
    for i,ir in enumerate([original,transformed]):
        lib=d/f'fixture{i}.so';run([bindir/'clang','-O3','-shared','-fPIC',ir,'-o',lib]);h=ctypes.CDLL(str(lib));f=h.outside;f.argtypes=[ctypes.POINTER(ctypes.c_int64),ctypes.c_uint64];f.restype=ctypes.c_bool;funcs.append((h,f))
    rng=random.Random(10943); boundaries=[-(2**63),-32769,-32768,-32767,-1,0,1,32766,32767,32768,2**63-1]
    for i in range(15000):
        vals=[rng.choice(boundaries) if i%2 else rng.randrange(-(2**63),2**63) for _ in range(8)];data=(ctypes.c_int64*8)(*vals);expected=any(x < -32768 or x > 32767 for x in vals);assert [f(data,1) for _,f in funcs]==[expected,expected],vals
    print('PASS boundaries and 15000 randomized arrays',flush=True)
    if shutil.which('z3'):
        assert run(['z3',ROOT/'scripts/signed16_range_equivalence.smt2'])=='unsat';print('PASS general 64-bit SMT equivalence',flush=True)
