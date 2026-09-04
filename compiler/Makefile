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
# Includes the fixture suites under ./test/ that are fast (analyzer/lexer-level, no native
# runtime) and green — gating them here stops the silent rot that let removed-syntax and
# stale-contract drift accumulate while nothing ran them. The backend fixture package is
# included now that its region-cleanup ownership regression is fixed.
FAST_PKGS := ./src/semantic/ ./src/smt/ ./src/backend/ ./src/parser/ ./src/lexer/ \
             ./src/interpreter/ ./src/easm/ ./src/frontendir/ ./src/unparse/ \
             ./test/backend/ ./test/semantic/ ./test/lexer/

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

# test-full: THE FULL SUITE — SLOW (~8 minutes; includes the elisacore/src native-runtime tests).
# Run this only as the final pre-commit gate, never in the inner loop.
.PHONY: test-full
test-full:
	@echo ">>> FULL SUITE — this is the ~8-minute run (native-runtime and fixture tests). <<<"
	go test -p 1 ./...

# build: build the local worktree binary.  Keep this target local so compiler
# dogfooding cannot overwrite the user's installed compiler or race another
# worktree.  Use `make install` only when an explicit installation is wanted.
.PHONY: build
build:
	mkdir -p bin
	go build -o bin/elisac ./src

# install: explicit opt-in installation for users who really want it.
.PHONY: install
# The binary is `elisac-stage0`, so a command line says which compiler it is
# using next to the self-hosted `elisac-stage1`. There is deliberately no bare
# `elisac`: a script that wants a compiler names its stage.
install:
	mkdir -p $${HOME}/.elisac
	go build -o $${HOME}/.elisac/elisac-stage0 ./src
	rm -f $${HOME}/.elisac/elisac

.PHONY: help
help:
	@echo "make test          fast inner-loop packages (~12s)        [default]"
	@echo "make test-semantic verification/prover blast radius (~10s)"
	@echo "make test-prover   semantic + smt"
	@echo "make test-full     FULL suite, SLOW ~8min (pre-commit only)"
	@echo "make build          build local compiler -> compiler/bin/elisac"
	@echo "make install        explicitly install -> ~/.elisac/elisac-stage0"
