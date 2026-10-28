//go:build !darwin && !linux

package main

import "os"

// Keep the same callback lifetime contract on platforms without the selected
// mmap implementation, using the portable validated reader.
func (a analysisDiskArtifact) withPayload(fn func([]byte) error) error {
	payload, err := a.read()
	if err != nil {
		return err
	}
	return fn(payload)
}

func (a analysisDiskArtifact) withBaselineSource(fn func([]byte) error) error {
	data, err := os.ReadFile(a.baselineSourcePath())
	if err != nil {
		return err
	}
	return fn(data)
}
