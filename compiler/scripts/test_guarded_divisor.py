#!/usr/bin/env python3
"""Check guarded divisor cloning, its rejection gates, and runtime behavior."""
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
CONFIG = os.environ.get('LLVM_CONFIG', '/opt/homebrew/opt/llvm/bin/llvm-config')

def run(cmd):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, timeout=28)

def flags(*args):
    return shlex.split(run([CONFIG, *args]).stdout)

DRIVER = r'''#include <llvm/IRReader/IRReader.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Support/FileSystem.h>
#include <llvm/Support/raw_ostream.h>
#include "llvm_guarded_divisor.h"
int main(int argc, char **argv) {
  llvm::LLVMContext c; llvm::SMDiagnostic error;
  auto m = llvm::parseIRFile(argv[1], error, c);
  if (!m) { error.print(argv[0], llvm::errs()); return 1; }
  if (llvm::verifyModule(*m, &llvm::errs())) return 2;
  auto *f = m->getFunction("work");
  bool changed = divisorSpecialize(*f);
  llvm::outs() << changed << "\n";
  if (llvm::verifyModule(*m, &llvm::errs())) return 3;
  std::error_code ec; llvm::raw_fd_ostream out(argv[2], ec);
  m->print(out, nullptr); return !!ec;
}
'''
HARNESS = r'''#include <stdint.h>
#include <stdio.h>
extern int64_t work(int64_t *, int64_t *, _Bool);
int main(void) {
  int64_t count = 0;
  printf("%lld ", (long long)work(0, &count, 0));
  for (int64_t i = 0; i < 18; ++i) {
    int64_t n = i / 2;
    printf("%lld:%lld ", (long long)work(&n, &count, i & 1), (long long)count);
  }
  printf("%lld\n", (long long)count);
}
'''

def replace_once(s, old, new):
    assert s.count(old) == 1, old
    return s.replace(old, new, 1)

base = (ROOT / 'test/fixtures/guarded_divisor_version.ll').read_text()
cases = {
    'covered': (base, 1),
    'volatile_load': (replace_once(base, '%raw = load i64', '%raw = load volatile i64'), 0),
    'single_remainder': (replace_once(base, '%r2 = urem i64 %i1, %div', '%r2 = add i64 %i1, %div'), 0),
    'external_call': (replace_once(base, 'prefix:\n', 'prefix:\n  call void @effect()\n') + '\ndeclare void @effect()\n', 0),
    'size_optimized': (replace_once(base, 'define i64 @work(ptr %input, ptr %count, i1 %skip) {', 'define i64 @work(ptr %input, ptr %count, i1 %skip) optsize {'), 0),
    'address_sanitizer': (replace_once(base, 'define i64 @work(ptr %input, ptr %count, i1 %skip) {', 'define i64 @work(ptr %input, ptr %count, i1 %skip) sanitize_address {'), 0),
    'complex_loop_metadata': (replace_once(base, '!1 = !{!"llvm.loop.unroll.disable"}', '!1 = !{!"llvm.loop.unroll.followup_all", !2}') + '\n!2 = distinct !{!2}\n', 0),
    'alias_scope': (replace_once(base, '%raw = load i64, ptr %input', '%raw = load i64, ptr %input, !alias.scope !2') + '\n!2 = !{!3}\n!3 = distinct !{!3, !4}\n!4 = distinct !{!4}\n', 0),
}
with tempfile.TemporaryDirectory(prefix='guarded-divisor-') as temp:
    d = Path(temp)
    (d/'driver.cpp').write_text(DRIVER)
    (d/'harness.c').write_text(HARNESS)
    bindir = Path(run([CONFIG, '--bindir']).stdout.strip())
    clang = str(bindir/'clang')
    run([str(bindir/'clang++'), '-Wno-deprecated-declarations', *flags('--cxxflags'),
         '-I'+str(ROOT/'src/backend/llvmnative/llvm_wrappers'),'-I'+str(ROOT/'src/backend/llvmnative'), str(d/'driver.cpp'),
         *flags('--ldflags', '--libs', 'core', 'irreader', 'analysis', 'transformutils', '--system-libs'),
         '-Wl,-rpath,'+run([CONFIG, '--libdir']).stdout.strip(), '-o', str(d/'driver')])
    for name, (ir, expected) in cases.items():
        source, transformed = d/(name+'.ll'), d/(name+'-out.ll')
        source.write_text(ir)
        changed = int(run([str(d/'driver'), str(source), str(transformed)]).stdout.strip())
        assert changed == expected, (name, changed, expected)
        if name == 'covered':
            for suffix, path in [('original', source), ('changed', transformed)]:
                run([clang, '-O2', str(path), str(d/'harness.c'), '-o', str(d/suffix)])
            assert run([str(d/'original')]).stdout == run([str(d/'changed')]).stdout
        print('PASS', name)
