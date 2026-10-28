package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"elisacore/src/semantic"
)

// A family is only a hint to a complete baseline. Its identity includes every
// exact-cache input except the included-file set. We accept only a strict subset
// or superset of source origins, keep the original artifact header, and always
// run checked scratch replay. Incomparable source sets and changes to trust,
// diagnostics, compiler, policy or target cannot share a hint.
type analysisDiskFamily struct {
	Version int
	Key     string
	Origins []string
}

func analysisDiskFamilyPath(program *loadedProgram, options semantic.AnalyzeOptions) (string, bool) {
	family, ok := analysisDiskArtifactForAuthority(program, options, false)
	if !ok {
		return "", false
	}
	return filepath.Join(filepath.Dir(family.path), family.key+".family"), true
}

func publishAnalysisDiskFamily(program *loadedProgram, options semantic.AnalyzeOptions, baseline analysisDiskArtifact) error {
	path, ok := analysisDiskFamilyPath(program, options)
	if !ok {
		return fmt.Errorf("ineligible analysis family")
	}
	origins := slices.Clone(program.sourceFiles)
	slices.Sort(origins)
	origins = slices.Compact(origins)
	data, err := json.Marshal(analysisDiskFamily{Version: 1, Key: baseline.key, Origins: origins})
	if err != nil {
		return err
	}
	if len(data) > 512<<10 {
		return fmt.Errorf("analysis family exceeds size limit")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".family-publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func analysisDiskFamilyBaseline(program *loadedProgram, options semantic.AnalyzeOptions) (analysisDiskArtifact, bool) {
	path, ok := analysisDiskFamilyPath(program, options)
	if !ok {
		return analysisDiskArtifact{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return analysisDiskArtifact{}, false
	}
	var family analysisDiskFamily
	if json.NewDecoder(io.LimitReader(file, 512<<10)).Decode(&family) != nil || family.Version != 1 || len(family.Key) != 64 {
		return analysisDiskArtifact{}, false
	}
	current := make(map[string]bool, len(program.sourceFiles))
	for _, origin := range program.sourceFiles {
		current[origin] = true
	}
	if len(current) != len(program.sourceFiles) || !slices.IsSorted(family.Origins) || len(family.Origins) == len(current) {
		return analysisDiskArtifact{}, false
	}
	previous := make(map[string]bool, len(family.Origins))
	for i, origin := range family.Origins {
		if i > 0 && family.Origins[i-1] == origin {
			return analysisDiskArtifact{}, false
		}
		previous[origin] = true
	}
	previousSubset := true
	for origin := range previous {
		previousSubset = previousSubset && current[origin]
	}
	currentSubset := true
	for origin := range current {
		currentSubset = currentSubset && previous[origin]
	}
	if !previousSubset && !currentSubset {
		return analysisDiskArtifact{}, false
	}
	// Derive the candidate from current authority plus the recorded old set.
	// Never trust a supplied path/key, and never re-label a snapshot envelope.
	priorProgram := *program
	priorProgram.sourceFiles = family.Origins
	candidate, ok := analysisDiskArtifactFor(&priorProgram, options)
	if !ok || candidate.key != family.Key {
		return analysisDiskArtifact{}, false
	}
	return candidate, true
}
