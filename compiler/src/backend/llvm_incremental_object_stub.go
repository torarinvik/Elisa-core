//go:build !cgo

package backend

import "elisacore/src/semantic"

func (s *IncrementalObjectSession) WriteObject(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) error {
	return WriteLLVMObjectFileWithOptions(result, outputPath, options)
}
