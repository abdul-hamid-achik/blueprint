package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteOutputFiles writes a generator's OutputFile list to outDir, with
// manifest tracking so that:
//
//   - files emitted by previous builds but no longer produced are removed
//     (so renamed/dropped output doesn't leave stale artifacts),
//   - files marked `UserOwned` are scaffolded only when missing and never
//     overwritten (so users can fill in native impls without losing them),
//   - paths are validated and stay within outDir (no `..`, no absolute escape),
//   - the manifest itself records sha256 hashes for every generated file.
//
// Every codegen target shells through this so the on-disk behavior is the
// same across `bp build --target node|python|...`.
func WriteOutputFiles(outDir string, files []OutputFile) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	manifest, err := loadOutputManifest(outDir)
	if err != nil {
		return err
	}

	generated := make(map[string]OutputFile)
	userOwned := make(map[string]OutputFile)
	for _, f := range files {
		rel, err := normalizeOutputPath(f.Path)
		if err != nil {
			return err
		}
		f.Path = rel
		if f.UserOwned {
			userOwned[rel] = f
			continue
		}
		generated[rel] = f
	}

	// Drop previously-generated files that this build no longer produces.
	for rel := range manifest.Generated {
		if _, stillGenerated := generated[rel]; stillGenerated {
			continue
		}
		if _, nowUserOwned := userOwned[rel]; nowUserOwned {
			continue
		}
		path := filepath.Join(outDir, filepath.FromSlash(rel))
		// Warn before removing if the on-disk copy was hand-edited since
		// the last build (hash differs from the manifest's recorded hash).
		// Without this, a user's hand edits to a generated file would be
		// silently lost when the file is no longer produced.
		warnIfStaleRemoved(outDir, rel, manifest.Generated[rel])
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale generated file %s: %w", path, err)
		}
	}

	for _, rel := range sortedOutputPaths(generated) {
		f := generated[rel]
		warnIfDrifted(outDir, rel, f.Content, manifest.Generated[rel])
		if err := writeFile(outDir, rel, f.Content); err != nil {
			return err
		}
	}

	for _, rel := range sortedOutputPaths(userOwned) {
		f := userOwned[rel]
		path := filepath.Join(outDir, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err == nil {
			continue // exists — leave the user's version alone
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if err := writeFile(outDir, rel, f.Content); err != nil {
			return err
		}
	}

	next := outputManifest{
		Version:   1,
		Generated: make(map[string]string, len(generated)),
	}
	for rel, f := range generated {
		next.Generated[rel] = "sha256:" + hashContent(f.Content)
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output manifest: %w", err)
	}
	data = append(data, '\n')
	return writeFile(outDir, outputManifestPath, data)
}

const outputManifestPath = ".blueprint/manifest.json"

type outputManifest struct {
	Version   int               `json:"version"`
	Generated map[string]string `json:"generated"`
}

func loadOutputManifest(outDir string) (outputManifest, error) {
	manifest := outputManifest{Version: 1, Generated: map[string]string{}}
	path := filepath.Join(outDir, outputManifestPath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return manifest, nil
	}
	if err != nil {
		return manifest, fmt.Errorf("read output manifest %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("parse output manifest %s: %w", path, err)
	}
	if manifest.Generated == nil {
		manifest.Generated = map[string]string{}
	}
	return manifest, nil
}

func normalizeOutputPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("invalid output path: empty")
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("invalid output path %q: must stay within output directory", path)
	}
	return filepath.ToSlash(clean), nil
}

// warnIfDrifted prints a stderr warning when the on-disk copy of a
// previously-generated file was hand-edited since the last build and this
// build is about to silently overwrite it. Rebuilds never refuse — this is a
// warning, not a gate — but it makes the clobber visible instead of losing
// hand edits without a trace. Silent when there's nothing to compare (first
// build for this path, prevHash == ""), when the on-disk file still matches
// the last recorded build (a clean rebuild), or when it already matches the
// new content (nothing would change).
func warnIfDrifted(outDir, rel string, newContent []byte, prevHash string) {
	if prevHash == "" {
		return // not tracked by a previous build — nothing to drift from
	}
	onDisk, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(rel)))
	if err != nil {
		return // nothing readable on disk yet — nothing to warn about
	}
	onDiskHash := "sha256:" + hashContent(onDisk)
	if onDiskHash == prevHash {
		return // matches the last build exactly — untouched
	}
	if newHash := "sha256:" + hashContent(newContent); onDiskHash == newHash {
		return // already matches the new content — nothing to warn about
	}
	fmt.Fprintf(os.Stderr, "%s was modified since last build; overwriting — put custom code in src/impl/ (UserOwned) or run `bp diff` first\n", rel)
}

// warnIfStaleRemoved prints a stderr warning when a previously-generated
// file that is about to be removed (no longer produced by this build) was
// hand-edited since the last build — the user's edits would be lost.
// Silent when there's nothing to compare or the on-disk file still matches
// the last recorded build (no hand edits to lose).
func warnIfStaleRemoved(outDir, rel, prevHash string) {
	if prevHash == "" {
		return // not tracked by a previous build
	}
	onDisk, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(rel)))
	if err != nil {
		return // nothing readable on disk
	}
	onDiskHash := "sha256:" + hashContent(onDisk)
	if onDiskHash == prevHash {
		return // matches the last build — no hand edits to lose
	}
	fmt.Fprintf(os.Stderr, "%s was modified since last build; removing (no longer generated) — hand edits will be lost\n", rel)
}

func writeFile(outDir, rel string, content []byte) error {
	path := filepath.Join(outDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func sortedOutputPaths(files map[string]OutputFile) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
