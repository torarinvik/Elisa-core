package main

import (
	"encoding/json"
	"io"

	golexer "elisacore/src/lexer"
)

// Canonical token-kind checksum (FNV-1a over token count then each kind+1).
// This is the SINGLE definition shared by the `-emit tokens` oracle and the
// frontend-lexer parity test, so the stage1 (Elisa) lexer can be checked against
// stage0 (this Go lexer) without two drifting copies of the algorithm.
const frontendTokenHashOffset uint64 = 1469598103934665603
const frontendTokenHashPrime uint64 = 1099511628211

func frontendTokenChecksumMix(hash uint64, value uint64) uint64 {
	return (hash ^ value) * frontendTokenHashPrime
}

// frontendTokenKindChecksum mixes the token count followed by each (kind+1).
// The +1 keeps TOKEN_EOF (kind 0) from being absorbed by the XOR.
func frontendTokenKindChecksum(kinds []golexer.TokenKind) uint64 {
	hash := frontendTokenChecksumMix(frontendTokenHashOffset, uint64(len(kinds)))
	for _, kind := range kinds {
		hash = frontendTokenChecksumMix(hash, uint64(kind)+1)
	}
	return hash
}

type emitTokensKind struct {
	Kind int    `json:"k"`
	Name string `json:"n"`
}

type emitTokensReport struct {
	File     string           `json:"file"`
	Count    int              `json:"count"`
	Checksum uint64           `json:"checksum"`
	Kinds    []emitTokensKind `json:"kinds"`
}

// runEmitTokens is the stage0 lexer oracle for cross-repo parity. It tokenizes
// the (include-expanded) program source with the Go lexer and prints a stable
// JSON report: token count, the canonical kind checksum, and the per-token kind
// ordinals+names. The stage1 Elisa lexer must reproduce the same checksum; the
// kinds list pinpoints the first divergent token when it does not.
func runEmitTokens(program *loadedProgram, stdout io.Writer, stderr io.Writer) int {
	if program == nil {
		return reportEmitTokensError(stderr, "missing program input")
	}
	tokens := golexer.New(program.filename, program.source).Tokenize()
	kinds := make([]golexer.TokenKind, len(tokens))
	report := emitTokensReport{
		File:  program.filename,
		Count: len(tokens),
		Kinds: make([]emitTokensKind, len(tokens)),
	}
	for i, tok := range tokens {
		kinds[i] = tok.Kind
		report.Kinds[i] = emitTokensKind{Kind: int(tok.Kind), Name: golexer.TokenName(tok.Kind)}
	}
	report.Checksum = frontendTokenKindChecksum(kinds)

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return reportEmitTokensError(stderr, err.Error())
	}
	return 0
}

func reportEmitTokensError(stderr io.Writer, message string) int {
	if stderr != nil {
		io.WriteString(stderr, "error: "+message+"\n")
	}
	return 1
}
