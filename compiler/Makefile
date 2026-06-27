# Elisa compiler — test ergonomics.
#
# WHY THIS EXISTS: `go test ./src/...` is ~8 minutes. 97% of that is ONE package
# (elisacore/src, ~453s) whose ~186 "runtime" tests each take an Elisa program all the
# way to a native binary (LLVM -> clang link -> execute). Every other package is <=10s
# or cached. So the fast inner loop is "run the dedicated packages, NOT ./src/...".
#
# Use `make test` (fast, ~12s) or `make test-semantic` (~10s, the verification/prover
# blast radius) while iterating. Run the full suite ONLY before committing: `make test-full`.

.DEFAULT_GOAL := test

# Fast packages: everything EXCEPT the heavy elisacore/src package. ~12s cold, instant cached.
FAST_PKGS := ./src/semantic/ ./src/smt/ ./src/backend/ ./src/parser/ ./src/lexer/ \
             ./src/interpreter/ ./src/grammar/ ./src/easm/ ./src/frontendir/ ./src/unparse/

# test: fast inner-loop suite. The default. Does NOT run the slow ./src native-runtime tests.
.PHONY: test
test:
	go test $(FAST_PKGS)

# test-semantic: tightest loop for verification-engine / prover work (~10s). The src/semantic
# package is the blast radius for analyzer changes (refinements, contracts, SMT discharge, flow).
.PHONY: test-semantic
test-semantic:
	go test ./src/semantic/

# test-prover: semantic + smt together.
.PHONY: test-prover
test-prover:
	go test ./src/semantic/ ./src/smt/

# test-full: THE FULL SUITE — SLOW (~8 minutes; the elisacore/src native-runtime tests).
# Run this only as the final pre-commit gate, never in the inner loop.
.PHONY: test-full
test-full:
	@echo ">>> FULL SUITE — this is the ~8-minute run (elisacore/src native-runtime tests). <<<"
	go test -p 1 ./src/...

# build: build + install the elisac binary.
.PHONY: build
build:
	mkdir -p $${HOME}/.elisac
	go build -o $${HOME}/.elisac/elisac ./src

.PHONY: help
help:
	@echo "make test          fast inner-loop packages (~12s)        [default]"
	@echo "make test-semantic verification/prover blast radius (~10s)"
	@echo "make test-prover   semantic + smt"
	@echo "make test-full     FULL suite, SLOW ~8min (pre-commit only)"
	@echo "make build         build+install elisac"
