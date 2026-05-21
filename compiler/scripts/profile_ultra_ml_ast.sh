#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPILER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_DIR="$(cd "${COMPILER_DIR}/.." && pwd)"

OUT_DIR=""
BASELINE_BENCHTIME="1x"
PROFILE_BENCHTIME="2s"
COUNT="1"
SAMPLE_SECONDS="5"
SCALAR_ITERATIONS="3"
PARALLEL_ITERATIONS="3"
PARALLEL_WORKERS="10"
CACHE_DEBUG="0"
RUN_BASELINES="1"
RUN_GO_PROFILES="1"
RUN_NATIVE_SAMPLES="1"
RUN_LLVM_INSPECT="1"

usage() {
  cat <<'EOF'
Usage: bash ./compiler/scripts/profile_ultra_ml_ast.sh [options]

Captures a profiling bundle for the Ultra ML AST benchmark family:
  1. baseline benchmark snapshots for Ultra backend/compile/native runtime
  2. Go CPU+heap profiles for backend IR generation and CLI object compile
  3. emitted Ultra LLVM IR plus helper-count summary
  4. native scalar/parallel samples on macOS using `sample`

Options:
  --output-dir DIR           Directory for artifacts (default: mktemp under /tmp)
  --baseline-benchtime ARG   Benchtime for baseline benchmark snapshots (default: 1x)
  --profile-benchtime ARG    Benchtime for Go profile captures (default: 2s)
  --count N                  Benchmark count for baseline snapshots (default: 1)
  --sample-seconds N         Seconds to capture with macOS `sample` (default: 5)
  --scalar-iterations N      Iterations for native scalar sample run (default: 3)
  --parallel-iterations N    Iterations for native parallel sample run (default: 3)
  --parallel-workers N       Worker count for native parallel sample run (default: 10)
  --cache-debug              Enable ELISACORE_CACHE_DEBUG=1 for Go benchmark runs
  --skip-baselines           Skip the baseline benchmark snapshot phase
  --skip-go-profiles         Skip Go CPU/heap profile generation
  --skip-native-samples      Skip macOS native `sample` captures
  --skip-llvm-inspect        Skip Ultra LLVM emit + helper summary
  -h, --help                 Show this help text
EOF
}

run_and_log() {
  local log_path="$1"
  shift
  echo
  echo "==> $*"
  "$@" 2>&1 | tee "$log_path"
}

write_note() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "$path"
}

write_pprof_top() {
  local profile_path="$1"
  local out_path="$2"
  if ! command -v go >/dev/null 2>&1; then
    write_note "$out_path" "go tool not available; unable to summarize $profile_path"
    return
  fi
  if ! go tool pprof -top -nodecount=40 "$profile_path" > "$out_path" 2>&1; then
    write_note "$out_path" "failed to summarize $profile_path with go tool pprof"
  fi
}

helper_count() {
  local needle="$1"
  local haystack="$2"
  grep -c "$needle" "$haystack" 2>/dev/null || true
}

emit_llvm_summary() {
  local llvm_path="$1"
  local summary_path="$2"
  {
    echo "llvm_file=$llvm_path"
    echo "llvm_bytes=$(wc -c < "$llvm_path" | tr -d ' ')"
    echo
    echo "function_headers:"
    grep -nE '^define .*@(score_type|score_pattern|score_expr|score_clause|score_stmt|score_decl|score_module|score_root|checksum_root_range|packed_ml_ast_repeated_checksum_impl|packed_ml_ast_parallel_checksum_impl)' "$llvm_path" || true
    echo
    echo "helper_counts:"
    for needle in \
      'ctx_packed_store_read_index_tag' \
      'ctx_packed_store_read_index_word' \
      'ctx_packed_store_read_word' \
      'ctx_packed_store_decode_index' \
      'ctx_packed_store_record_prefix_words' \
      'ctx_packed_store_alloc_fixed_tagged_index_result'; do
      echo "${needle}=$(helper_count "$needle" "$llvm_path")"
    done
  } > "$summary_path"
}

build_native_ultra() {
  local build_dir="$1"
  local log_path="$2"
  local symbol_path="$3"

  local header_path="${build_dir}/packed_lowering_ml_ast_mega_core.h"
  local object_path="${build_dir}/packed_lowering_ml_ast_ultra_core.o"
  local exe_path="${build_dir}/packed_lowering_ml_ast_ultra_bench"

  mkdir -p "$build_dir"

  {
    echo "build_dir=$build_dir"
    echo "header_path=$header_path"
    echo "object_path=$object_path"
    echo "exe_path=$exe_path"
    cd "$COMPILER_DIR"
    go run ./src -O3 -emit header -o "$header_path" ../Code/benchmarks/packed_lowering_ml_ast_ultra_core.elisa
    go run ./src -O3 -emit obj -o "$object_path" ../Code/benchmarks/packed_lowering_ml_ast_ultra_core.elisa
    clang -O3 -pthread -Wl,-undefined,dynamic_lookup -I "$build_dir" \
      ../Code/benchmarks/packed_lowering_ml_ast_bench.c \
      ../Code/benchmarks/json_parser_runtime_shims.c \
      "$object_path" \
      -o "$exe_path"
  } 2>&1 | tee "$log_path" >&2

  {
    echo "object_symbols:"
    nm "$object_path" | grep -E 'score_|packed_ml_ast_|ml_ast_parallel_' || true
    echo
    echo "executable_symbols:"
    nm "$exe_path" | grep -E 'score_|packed_ml_ast_|ml_ast_parallel_' || true
  } > "$symbol_path"

  printf '%s\n' "$exe_path"
}

sample_native_run() {
  local exe_path="$1"
  local mode="$2"
  local iterations="$3"
  local workers="$4"
  local sample_seconds="$5"
  local run_log="$6"
  local sample_out="$7"

  if [[ "$(uname -s)" != "Darwin" ]]; then
    write_note "$sample_out" "native sampling skipped: sample is only supported on macOS"
    return
  fi
  if ! command -v sample >/dev/null 2>&1; then
    write_note "$sample_out" "native sampling skipped: sample command not available"
    return
  fi

  echo
  echo "==> sampling native ${mode} run (${iterations} iterations, ${sample_seconds}s capture)"
  if [[ "$mode" == "parallel" ]]; then
    "$exe_path" "$mode" "$iterations" "$workers" > "$run_log" 2>&1 &
  else
    "$exe_path" "$mode" "$iterations" > "$run_log" 2>&1 &
  fi
  local run_pid=$!

  sleep 1
  if ! kill -0 "$run_pid" 2>/dev/null; then
    wait "$run_pid"
    write_note "$sample_out" "native sampling skipped: process exited before sample could attach"
    return
  fi

  if ! sample "$run_pid" "$sample_seconds" -file "$sample_out" >/dev/null 2>&1; then
    echo "warning: sample failed for pid=$run_pid" | tee -a "$run_log"
  fi
  wait "$run_pid"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-dir)
      OUT_DIR="${2:?missing value for --output-dir}"
      shift 2
      ;;
    --baseline-benchtime)
      BASELINE_BENCHTIME="${2:?missing value for --baseline-benchtime}"
      shift 2
      ;;
    --profile-benchtime)
      PROFILE_BENCHTIME="${2:?missing value for --profile-benchtime}"
      shift 2
      ;;
    --count)
      COUNT="${2:?missing value for --count}"
      shift 2
      ;;
    --sample-seconds)
      SAMPLE_SECONDS="${2:?missing value for --sample-seconds}"
      shift 2
      ;;
    --scalar-iterations)
      SCALAR_ITERATIONS="${2:?missing value for --scalar-iterations}"
      shift 2
      ;;
    --parallel-iterations)
      PARALLEL_ITERATIONS="${2:?missing value for --parallel-iterations}"
      shift 2
      ;;
    --parallel-workers)
      PARALLEL_WORKERS="${2:?missing value for --parallel-workers}"
      shift 2
      ;;
    --cache-debug)
      CACHE_DEBUG="1"
      shift
      ;;
    --skip-baselines)
      RUN_BASELINES="0"
      shift
      ;;
    --skip-go-profiles)
      RUN_GO_PROFILES="0"
      shift
      ;;
    --skip-native-samples)
      RUN_NATIVE_SAMPLES="0"
      shift
      ;;
    --skip-llvm-inspect)
      RUN_LLVM_INSPECT="0"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$OUT_DIR" ]]; then
  OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elisacore-ultra-profile.XXXXXX")"
else
  mkdir -p "$OUT_DIR"
fi

if [[ "$CACHE_DEBUG" == "1" ]]; then
  export ELISACORE_CACHE_DEBUG=1
else
  export ELISACORE_CACHE_DEBUG=""
fi

echo "output_dir=$OUT_DIR"
cd "$COMPILER_DIR"

if [[ "$RUN_BASELINES" == "1" ]]; then
  run_and_log "$OUT_DIR/bench_backend_ultra.log" \
    go test ./src/backend -run '^$' -bench '^(BenchmarkGenerateLLVMIRPackedLoweringMLASTUltraRetainedReads|BenchmarkGenerateLLVMIRPackedLoweringMLASTUltraParallelRetainedReads)$' -benchmem -benchtime="$BASELINE_BENCHTIME" -count="$COUNT"

  run_and_log "$OUT_DIR/bench_compile_ultra.log" \
    go test ./src -run '^$' -bench '^BenchmarkRunCLICompilePackedMLASTUltraToObjectO3$' -benchmem -benchtime="$BASELINE_BENCHTIME" -count="$COUNT"

  run_and_log "$OUT_DIR/bench_native_ultra.log" \
    go test ./src -run '^$' -bench '^(BenchmarkRunNativePackedMLASTUltraRuntime|BenchmarkRunNativePackedMLASTUltraRuntimeParallel10)$' -benchmem -benchtime="$BASELINE_BENCHTIME" -count="$COUNT"
fi

if [[ "$RUN_GO_PROFILES" == "1" ]]; then
  run_and_log "$OUT_DIR/profile_go_backend_ultra.log" \
    go test ./src/backend -run '^$' -bench '^BenchmarkGenerateLLVMIRPackedLoweringMLASTUltraRetainedReads$' -benchtime="$PROFILE_BENCHTIME" -count=1 -cpuprofile "$OUT_DIR/go_backend_ultra_cpu.pprof" -memprofile "$OUT_DIR/go_backend_ultra_mem.pprof"
  write_pprof_top "$OUT_DIR/go_backend_ultra_cpu.pprof" "$OUT_DIR/go_backend_ultra_cpu_top.txt"
  write_pprof_top "$OUT_DIR/go_backend_ultra_mem.pprof" "$OUT_DIR/go_backend_ultra_mem_top.txt"

  run_and_log "$OUT_DIR/profile_go_compile_ultra.log" \
    go test ./src -run '^$' -bench '^BenchmarkRunCLICompilePackedMLASTUltraToObjectO3$' -benchtime="$PROFILE_BENCHTIME" -count=1 -cpuprofile "$OUT_DIR/go_compile_ultra_cpu.pprof" -memprofile "$OUT_DIR/go_compile_ultra_mem.pprof"
  write_pprof_top "$OUT_DIR/go_compile_ultra_cpu.pprof" "$OUT_DIR/go_compile_ultra_cpu_top.txt"
  write_pprof_top "$OUT_DIR/go_compile_ultra_mem.pprof" "$OUT_DIR/go_compile_ultra_mem_top.txt"
fi

if [[ "$RUN_LLVM_INSPECT" == "1" ]]; then
  echo
  echo "==> emitting Ultra LLVM IR"
  LLVM_OUT="$OUT_DIR/packed_lowering_ml_ast_ultra.ll"
  go run ./src -O3 -emit llvm -o "$LLVM_OUT" ../Code/benchmarks/packed_lowering_ml_ast_ultra_bench.elisa 2>&1 | tee "$OUT_DIR/emit_ultra_llvm.log"
  emit_llvm_summary "$LLVM_OUT" "$OUT_DIR/emit_ultra_llvm_summary.txt"
fi

if [[ "$RUN_NATIVE_SAMPLES" == "1" ]]; then
  NATIVE_BUILD_DIR="$OUT_DIR/native_build"
  NATIVE_EXE="$(build_native_ultra "$NATIVE_BUILD_DIR" "$OUT_DIR/native_build.log" "$OUT_DIR/native_symbols.txt")"
  sample_native_run "$NATIVE_EXE" scalar "$SCALAR_ITERATIONS" 1 "$SAMPLE_SECONDS" "$OUT_DIR/native_scalar_run.log" "$OUT_DIR/native_scalar.sample.txt"
  sample_native_run "$NATIVE_EXE" parallel "$PARALLEL_ITERATIONS" "$PARALLEL_WORKERS" "$SAMPLE_SECONDS" "$OUT_DIR/native_parallel_run.log" "$OUT_DIR/native_parallel.sample.txt"
fi

cat > "$OUT_DIR/SUMMARY.txt" <<EOF
Ultra ML AST profiling bundle
==============================

output_dir: $OUT_DIR
baseline_benchtime: $BASELINE_BENCHTIME
profile_benchtime: $PROFILE_BENCHTIME
sample_seconds: $SAMPLE_SECONDS
scalar_iterations: $SCALAR_ITERATIONS
parallel_iterations: $PARALLEL_ITERATIONS
parallel_workers: $PARALLEL_WORKERS

Key artifacts
-------------
bench_backend_ultra.log            Ultra backend IR baseline benchmark output
bench_compile_ultra.log            Ultra object compile baseline benchmark output
bench_native_ultra.log             Ultra scalar/parallel native baseline benchmark output
go_backend_ultra_cpu.pprof         Go CPU profile for backend IR generation
go_backend_ultra_cpu_top.txt       Text summary for backend CPU profile
go_backend_ultra_mem.pprof         Go heap profile for backend IR generation
go_compile_ultra_cpu.pprof         Go CPU profile for Ultra object compile
go_compile_ultra_cpu_top.txt       Text summary for Ultra compile CPU profile
go_compile_ultra_mem.pprof         Go heap profile for Ultra object compile
packed_lowering_ml_ast_ultra.ll    Emitted Ultra LLVM IR
emit_ultra_llvm_summary.txt        LLVM helper counts + hot function headers
native_build.log                   Manual Ultra native build log
native_symbols.txt                 Native symbol list for score_* / packed_ml_ast_* entrypoints
native_scalar_run.log              Native scalar runtime output
native_scalar.sample.txt           macOS sample capture for scalar runtime
native_parallel_run.log            Native parallel runtime output
native_parallel.sample.txt         macOS sample capture for parallel runtime

Suggested next steps
--------------------
go tool pprof -http=:0 $OUT_DIR/go_backend_ultra_cpu.pprof
go tool pprof -http=:0 $OUT_DIR/go_compile_ultra_cpu.pprof
open $OUT_DIR/native_scalar.sample.txt
open $OUT_DIR/native_parallel.sample.txt
EOF

echo
echo "profiling bundle ready: $OUT_DIR"