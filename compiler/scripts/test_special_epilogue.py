#!/usr/bin/env python3
"""Exercise both paths of the bounded validation epilogue specialization."""
import ctypes
import os
import shlex
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if os.uname().machine not in ("arm64", "aarch64"):
    print("SKIP: the pass targets AArch64")
    raise SystemExit(0)
CONFIG = os.environ.get("LLVM_CONFIG", "/opt/homebrew/opt/llvm@23/bin/llvm-config" if os.uname().sysname == "Darwin" else "/usr/lib/llvm-23/bin/llvm-config")
TIMEOUT = "gtimeout" if os.uname().sysname == "Darwin" else "timeout"

def run(args):
    p = subprocess.run([TIMEOUT, "-k", "1s", "28s", *map(str, args)], capture_output=True, text=True)
    if p.returncode:
        raise RuntimeError(f"{args}: {p.stderr}")
    return p.stdout.strip()

def flags(*args):
    return shlex.split(run([CONFIG, *args]))

def fixture():
    triple = "arm64-apple-darwin" if os.uname().sysname == "Darwin" else "aarch64-unknown-linux-gnu"
    out = [f'target triple = "{triple}"', 'declare void @llvm.assume(i1)', 'define i64 @scan(ptr %p, i64 %n) {', 'entry:', '  %rem = and i64 %n, 15', '  %nz = icmp ne i64 %rem, 0', '  call void @llvm.assume(i1 %nz)', '  br label %load0']
    for i in range(15):
        out += [f'load{i}:', f'  %p{i} = getelementptr i64, ptr %p, i64 {i}', f'  %v{i} = load i64, ptr %p{i}, align 8', f'  %bad{i} = icmp slt i64 %v{i}, 0', f'  br i1 %bad{i}, label %fail, label %check{i}', f'check{i}:', f'  %done{i} = icmp eq i64 %rem, {i+1}', f'  br i1 %done{i}, label %success, label %'+(f'load{i+1}' if i<14 else 'success')]
    return '\n'.join(out + ['fail:', '  ret i64 -1', 'success:', '  ret i64 0', '}', ''])

DRIVER = r'''
#include <llvm-c/Core.h>
#include <llvm/IR/LLVMContext.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Support/raw_ostream.h>
extern "C" unsigned elisacoreSpecializeValidationEpilogues(LLVMModuleRef);
int main(int argc, char **argv) {
 llvm::LLVMContext c; llvm::SMDiagnostic d; auto m=llvm::parseIRFile(argv[1],d,c);
 if(!m){d.print(argv[0],llvm::errs());return 2;}
 auto a=elisacoreSpecializeValidationEpilogues(llvm::wrap(m.get()));
 auto b=elisacoreSpecializeValidationEpilogues(llvm::wrap(m.get()));
 if(llvm::verifyModule(*m,&llvm::errs()))return 3;
 std::error_code e;llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);
 llvm::outs()<<a<<" "<<b;return e?4:0;
}
'''
with tempfile.TemporaryDirectory(prefix="elisa-epilogue-") as tmp:
    d=Path(tmp); bindir=Path(run([CONFIG,"--bindir"]));libdir=run([CONFIG,"--libdir"])
    (d/'driver.cpp').write_text(DRIVER)
    run([bindir/'clang++',*flags('--cxxflags'),d/'driver.cpp',ROOT/'src/backend/llvm_wrappers/llvm_special_epilogue.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
    source=fixture()
    negatives={
        'volatile':source.replace('%v3 = load i64','%v3 = load volatile i64'),
        'different mask':source.replace('%rem = and i64 %n, 15','%rem = and i64 %n, 7'),
        'different iteration test':source.replace('%done3 = icmp eq i64 %rem, 4','%done3 = icmp eq i64 %rem, 5'),
        'missing assumption':source.replace('  call void @llvm.assume(i1 %nz)\n',''),
        'side effect':source.replace('  %v3 = load i64','  store i64 1, ptr %p, align 8\n  %v3 = load i64'),
        'sanitizer':source.replace('define i64 @scan(ptr %p, i64 %n) {','define i64 @scan(ptr %p, i64 %n) sanitize_address {'),
    }
    for name,ir in [('eligible',source),*negatives.items()]:
        inp=d/(name.replace(' ','_')+'.ll');out=d/(name.replace(' ','_')+'.out.ll');inp.write_text(ir)
        got=run([d/'driver',inp,out]);expected='1 0' if name=='eligible' else '0 0'
        assert got==expected,(name,got,expected)
        print('PASS',name,flush=True)
    libs=[]
    for name in ['eligible','eligible.out']:
        ir=d/(name+'.ll');lib=d/(name+'.so')
        run([bindir/'clang','-O3','-shared','-fPIC',ir,'-o',lib])
        handle=ctypes.CDLL(str(lib));f=handle.scan;f.argtypes=[ctypes.POINTER(ctypes.c_int64),ctypes.c_uint64];f.restype=ctypes.c_int64;libs.append(f)
    for n in range(1,16):
        for bad in range(-1,n):
            data=[1]*n
            if bad>=0:data[bad]=-1
            buf=(ctypes.c_int64*n)(*data);expected=-1 if bad>=0 else 0
            assert [f(buf,n) for f in libs]==[expected,expected],(n,bad)
    print('PASS all remainder and invalid positions',flush=True)
    libc=ctypes.CDLL(None);libc.mmap.restype=ctypes.c_void_p;libc.mmap.argtypes=[ctypes.c_void_p,ctypes.c_size_t,ctypes.c_int,ctypes.c_int,ctypes.c_int,ctypes.c_long];libc.mprotect.argtypes=[ctypes.c_void_p,ctypes.c_size_t,ctypes.c_int]
    page=os.sysconf('SC_PAGESIZE');mapanon=0x1000 if os.uname().sysname=='Darwin' else 0x20
    for n in range(1,16):
        for bad in range(n):
            pid=os.fork()
            if pid==0:
                addr=libc.mmap(None,page*2,3,2|mapanon,-1,0)
                if addr in (None,ctypes.c_void_p(-1).value) or libc.mprotect(addr+page,page,0):os._exit(2)
                start=addr+page-(bad+1)*8;arr=(ctypes.c_int64*(bad+1)).from_address(start)
                for i in range(bad):arr[i]=1
                arr[bad]=-1
                os._exit(0 if libs[1](ctypes.cast(start,ctypes.POINTER(ctypes.c_int64)),n)==-1 else 3)
            _,status=os.waitpid(pid,0)
            assert os.WIFEXITED(status) and os.WEXITSTATUS(status)==0,(n,bad,status)
    print('PASS guarded early exits on both paths',flush=True)
