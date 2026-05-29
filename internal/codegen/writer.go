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
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale generated file %s: %w", path, err)
		}
	}

	for _, rel := range sortedOutputPaths(generated) {
		f := generated[rel]
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
