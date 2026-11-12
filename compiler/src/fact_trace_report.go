package main

import (
	"elisacore/src/facttrace"
	"elisacore/src/semantic"
)

func generateFactTraceReport(result *semantic.Result, filter string) (string, error) {
	return facttrace.Generate(result, filter)
}

func indentReportBlock(text, prefix string) string { return facttrace.IndentReportBlock(text, prefix) }
