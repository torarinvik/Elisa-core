#!/usr/bin/env python3
"""Verify scheduler selection delegates unmarked functions to LLVM's default."""
import os, shlex, subprocess, tempfile
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
if os.uname().sysname != 'Linux' or os.uname().machine not in ('aarch64', 'arm64'):
    print('SKIP: selective scheduler is used on Linux AArch64')
    raise SystemExit(0)
CONFIG = os.environ.get('LLVM_CONFIG', '/usr/lib/llvm-23/bin/llvm-config')
def run(args):
    p = subprocess.run(['timeout', '-k', '1s', '28s', *map(str, args)], capture_output=True, text=True)
    if p.returncode:
        raise RuntimeError(f'{args}: {p.stderr}')
    return p
def flags(*args):
    return shlex.split(run([CONFIG, *args]).stdout)
IR = '''source_filename = "scheduler-test"
target triple = "aarch64-unknown-linux-gnu"
define i64 @f(ptr %p, i64 %x) #0 {
entry:
 %a = load i64, ptr %p, align 8
 %p1 = getelementptr i64, ptr %p, i64 1
 %b = load i64, ptr %p1, align 8
 %c = mul i64 %a, %x
 %d = mul i64 %b, %x
 %e = add i64 %c, %d
 ret i64 %e
}
attributes #0 = { nounwind ATTR }
'''
with tempfile.TemporaryDirectory(prefix='selective-scheduler-') as tmp:
    d = Path(tmp)
    bindir = Path(run([CONFIG, '--bindir']).stdout.strip())
    plugin = d / 'scheduler.so'
    libdir = run([CONFIG, '--libdir']).stdout.strip()
    run([bindir/'clang++', *flags('--cxxflags'), '-fPIC', '-shared', ROOT/'src/backend/llvmnative/llvm_wrappers/llvm_selective_scheduler.cpp', *flags('--ldflags','--libs','--system-libs'), '-Wl,-rpath,'+libdir, '-o', plugin])
    def emit(tagged, scheduler, load, name):
        path = d / ('tagged.ll' if tagged else 'plain.ll')
        path.write_text(IR.replace('ATTR', '"elisa.ordered.ilp"' if tagged else ''))
        out = d / name
        args = [bindir/'llc', '-mtriple=aarch64-linux-gnu', '-mcpu=generic', '-O3', '-filetype=obj']
        if load:
            args += ['-load='+str(plugin)]
        if scheduler:
            args += ['-misched='+scheduler]
        run([*args, path, '-o', out])
        return out.read_bytes()
    assert emit(False, None, False, 'plain.o') == emit(False, 'elisa-ordered-selective', True, 'selected-plain.o')
    assert emit(True, 'ilpmax', False, 'ilp.o') == emit(True, 'elisa-ordered-selective', True, 'selected-ilp.o')
    print('PASS unmarked target-default scheduler')
    print('PASS marked ILP scheduler')
