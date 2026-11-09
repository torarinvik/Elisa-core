# Machine-check the EASM mechanized core (docs/106).
# Requires The Rocq Prover (>= 9.0). `make` exits 0 iff progress + preservation check.
ROCQ ?= rocq

.PHONY: check clean
check: EasmTAL.vo

EasmTALMachine.vo: EasmTALMachine.v
	$(ROCQ) compile $<

EasmTAL.vo: EasmTAL.v EasmTALMachine.vo
	$(ROCQ) compile $<

clean:
	rm -f *.vo *.vok *.vos *.glob .*.aux
