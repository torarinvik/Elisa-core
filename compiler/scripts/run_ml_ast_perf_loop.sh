#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPILER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

BENCHTIME="1x"
COUNT="1"
RUN_MEGA="0"
CACHE_DEBUG="0"

usage() {
  cat <<'EOF'
Usage: ./scripts/run_ml_ast_perf_loop.sh [--benchtime 1x] [--count 1] [--mega] [--cache-debug]

Runs the everyday ML AST performance loop:
  1. reduced repro + medium native smoke
  2. medium backend retained-reads benchmarks
  3. medium CLI compile + native runtime benchmarks

With --mega it also runs the explicit slow-path mega validation lane.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --benchtime)
      BENCHTIME="${2:?missing value for --benchtime}"
      shift 2
      ;;
    --count)
      COUNT="${2:?missing value for --count}"
      shift 2
      ;;
    --mega)
      RUN_MEGA="1"
      shift
      ;;
    --cache-debug)
      CACHE_DEBUG="1"
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

run_cmd() {
  echo
  echo "==> $*"
  "$@"
}

export ELISACORE_CACHE_DEBUG=""
if [[ "${CACHE_DEBUG}" == "1" ]]; then
  export ELISACORE_CACHE_DEBUG=1
fi

cd "${COMPILER_DIR}"

run_cmd go test ./src -run '^(TestRunCLIPackedMLExprReproSmoke|TestRunCLIPackedMLASTBenchSmoke)$' -count="${COUNT}"
run_cmd go test ./src/backend -run '^$' -bench '^(BenchmarkGenerateLLVMIRPackedLoweringMLAST(RetainedReads|ParallelRetainedReads))$' -benchtime="${BENCHTIME}" -count="${COUNT}"
run_cmd go test ./src -run '^$' -bench '^(BenchmarkRunCLICompilePackedMLAST(ToLLVM|Parallel10ToLLVM|ToObjectO3)|BenchmarkRunNativePackedMLASTRuntime(Parallel10)?)$' -benchtime="${BENCHTIME}" -count="${COUNT}"

if [[ "${RUN_MEGA}" == "1" ]]; then
  echo
  echo "==> running explicit mega validation lane"
  run_cmd env ELISACORE_SLOW_NATIVE=1 go test ./src -run '^TestRunCLIPackedMLASTMegaBenchSmoke$' -count="${COUNT}"
  run_cmd go test ./src/backend -run '^$' -bench '^(BenchmarkGenerateLLVMIRPackedLoweringMLASTMegaRetainedReads|BenchmarkGenerateLLVMIRPackedLoweringMLASTMegaParallelRetainedReads)$' -benchtime="${BENCHTIME}" -count="${COUNT}"
  run_cmd go test ./src -run '^$' -bench '^(BenchmarkRunCLICompilePackedMLASTMegaToObjectO3|BenchmarkRunNativePackedMLASTMegaRuntime)$' -benchtime="${BENCHTIME}" -count="${COUNT}"
fi
