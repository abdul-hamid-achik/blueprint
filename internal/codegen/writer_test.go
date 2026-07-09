package codegen

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. WriteOutputFiles's drift warning goes straight
// to os.Stderr (it's a CLI-facing notice, not a returned error), so tests
// asserting on it need to intercept the fd.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// TestLoadOutputManifestMissing covers the case when no manifest file exists
// on disk — loadOutputManifest should return an empty default cleanly with no
// error. This is the "first build into a fresh outDir" case.
func TestLoadOutputManifestMissing(t *testing.T) {
	dir := t.TempDir()

	m, err := loadOutputManifest(dir)
	if err != nil {
		t.Fatalf("loadOutputManifest on missing manifest returned error: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version = %d, want 1", m.Version)
	}
	if m.Generated == nil {
		t.Errorf("Generated map is nil, want empty initialized map")
	}
	if len(m.Generated) != 0 {
		t.Errorf("Generated has %d entries, want 0", len(m.Generated))
	}
}

// TestLoadOutputManifestMalformed documents the current behavior when the
// manifest on disk is corrupt JSON — loadOutputManifest returns an error
// wrapping json.Unmarshal's failure. The returned manifest is still the
// empty default (populated with version=1 and empty Generated map), so
// callers that ignore the error can degrade gracefully.
func TestLoadOutputManifestMalformed(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, outputManifestPath)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	m, err := loadOutputManifest(dir)
	if err == nil {
		t.Fatalf("loadOutputManifest on malformed manifest returned no error; want a parse error")
	}
	if !strings.Contains(err.Error(), "parse output manifest") {
		t.Errorf("error %q does not mention 'parse output manifest'", err.Error())
	}
	// Even on error, the returned manifest should still be safely usable as
	// an empty default — Generated should be non-nil because the function
	// pre-initializes it before unmarshal.
	if m.Generated == nil {
		t.Errorf("Generated map is nil after malformed manifest; want empty default")
	}
}

// TestWriteOutputFilesCleansStaleEntries is the regression for the
// node_modules-false-positive bug (commit 1fb6583). The cleanup pass must:
//
//   - delete files previously listed in the manifest that the current build
//     no longer produces, AND
//   - leave alone any file on disk that was NOT in the manifest (e.g. user
//     deps like node_modules/, .env, etc.).
func TestWriteOutputFilesCleansStaleEntries(t *testing.T) {
	dir := t.TempDir()

	// First build: emit A, B, C — all tracked in the manifest.
	first := []OutputFile{
		{Path: "a.txt", Content: []byte("A1")},
		{Path: "b.txt", Content: []byte("B1")},
		{Path: "c.txt", Content: []byte("C1")},
	}
	if err := WriteOutputFiles(dir, first); err != nil {
		t.Fatalf("first WriteOutputFiles: %v", err)
	}

	// Drop a file D that the build never produced — simulates user-managed
	// content (node_modules, .env, hand-written impls in subdirs, etc.).
	dPath := filepath.Join(dir, "d.txt")
	if err := os.WriteFile(dPath, []byte("user-owned"), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}
	// Also drop a nested untracked file (covers the node_modules case).
	nmDir := filepath.Join(dir, "node_modules", "lodash")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatalf("seed nested dir: %v", err)
	}
	nmFile := filepath.Join(nmDir, "index.js")
	if err := os.WriteFile(nmFile, []byte("module.exports = {}"), 0o644); err != nil {
		t.Fatalf("seed nested file: %v", err)
	}

	// Second build: only A, B. C should be removed (was in manifest, now
	// missing). D and nested untracked content must survive.
	second := []OutputFile{
		{Path: "a.txt", Content: []byte("A2")},
		{Path: "b.txt", Content: []byte("B2")},
	}
	if err := WriteOutputFiles(dir, second); err != nil {
		t.Fatalf("second WriteOutputFiles: %v", err)
	}

	// C must be gone.
	if _, err := os.Stat(filepath.Join(dir, "c.txt")); !os.IsNotExist(err) {
		t.Errorf("c.txt still exists after second build (err=%v); want removed", err)
	}
	// A and B must be present with new content.
	if got, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(got) != "A2" {
		t.Errorf("a.txt = %q err=%v; want %q", string(got), err, "A2")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "b.txt")); err != nil || string(got) != "B2" {
		t.Errorf("b.txt = %q err=%v; want %q", string(got), err, "B2")
	}
	// D must survive — never in manifest, must not be touched.
	if got, err := os.ReadFile(dPath); err != nil || string(got) != "user-owned" {
		t.Errorf("d.txt = %q err=%v; want %q (untouched)", string(got), err, "user-owned")
	}
	// node_modules content must survive.
	if got, err := os.ReadFile(nmFile); err != nil || string(got) != "module.exports = {}" {
		t.Errorf("node_modules/lodash/index.js = %q err=%v; want untouched", string(got), err)
	}
}

// TestWriteOutputFilesUserOwnedSurvives verifies UserOwned semantics:
// scaffold the file once, but on subsequent emits leave the user's edits
// alone even if the generator's content would differ.
func TestWriteOutputFilesUserOwnedSurvives(t *testing.T) {
	dir := t.TempDir()

	// First emit: scaffold the user-owned file.
	first := []OutputFile{
		{Path: "impl.ts", Content: []byte("// scaffold\n"), UserOwned: true},
	}
	if err := WriteOutputFiles(dir, first); err != nil {
		t.Fatalf("first WriteOutputFiles: %v", err)
	}
	implPath := filepath.Join(dir, "impl.ts")
	if got, err := os.ReadFile(implPath); err != nil || string(got) != "// scaffold\n" {
		t.Fatalf("scaffold mismatch: got=%q err=%v", string(got), err)
	}

	// User modifies the file in place.
	userContent := []byte("// my hand-written impl\nexport const x = 42;\n")
	if err := os.WriteFile(implPath, userContent, 0o644); err != nil {
		t.Fatalf("user edit: %v", err)
	}

	// Second emit: same path, regenerated content differs from scaffold.
	second := []OutputFile{
		{Path: "impl.ts", Content: []byte("// regenerated\n"), UserOwned: true},
	}
	if err := WriteOutputFiles(dir, second); err != nil {
		t.Fatalf("second WriteOutputFiles: %v", err)
	}

	got, err := os.ReadFile(implPath)
	if err != nil {
		t.Fatalf("read impl after second emit: %v", err)
	}
	if string(got) != string(userContent) {
		t.Errorf("impl.ts = %q; want user's version %q (UserOwned files must not be overwritten)",
			string(got), string(userContent))
	}
}

// TestWriteOutputFilesWarnsOnDrift covers the "rebuild silently destroys hand
// edits" bug: when a previously-generated file was hand-edited on disk
// (differs from both the last recorded hash and the freshly generated
// content), WriteOutputFiles must print a stderr warning naming the file
// before overwriting it. It's a warning, not a refusal — the write still
// happens with the new generated content.
func TestWriteOutputFilesWarnsOnDrift(t *testing.T) {
	dir := t.TempDir()

	first := []OutputFile{{Path: "src/index.ts", Content: []byte("// v1\n")}}
	if err := WriteOutputFiles(dir, first); err != nil {
		t.Fatalf("first WriteOutputFiles: %v", err)
	}

	path := filepath.Join(dir, "src", "index.ts")
	if err := os.WriteFile(path, []byte("// hand-edited\n"), 0o644); err != nil {
		t.Fatalf("hand-edit: %v", err)
	}

	stderr := captureStderr(t, func() {
		second := []OutputFile{{Path: "src/index.ts", Content: []byte("// v2\n")}}
		if err := WriteOutputFiles(dir, second); err != nil {
			t.Fatalf("second WriteOutputFiles: %v", err)
		}
	})

	if !strings.Contains(stderr, "src/index.ts") {
		t.Errorf("expected drift warning to name src/index.ts, got: %q", stderr)
	}
	if !strings.Contains(stderr, "modified since last build") {
		t.Errorf("expected drift warning text, got: %q", stderr)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "// v2\n" {
		t.Errorf("src/index.ts = %q err=%v; want overwritten with %q (warning, not refusal)", string(got), err, "// v2\n")
	}
}

// TestWriteOutputFilesSilentOnCleanRebuild verifies a rebuild where the
// on-disk file still matches the last recorded hash produces no warning —
// the common case (no hand edits) must stay quiet.
func TestWriteOutputFilesSilentOnCleanRebuild(t *testing.T) {
	dir := t.TempDir()

	first := []OutputFile{{Path: "src/index.ts", Content: []byte("// v1\n")}}
	if err := WriteOutputFiles(dir, first); err != nil {
		t.Fatalf("first WriteOutputFiles: %v", err)
	}

	stderr := captureStderr(t, func() {
		second := []OutputFile{{Path: "src/index.ts", Content: []byte("// v2\n")}}
		if err := WriteOutputFiles(dir, second); err != nil {
			t.Fatalf("second WriteOutputFiles: %v", err)
		}
	})

	if strings.Contains(stderr, "modified since last build") {
		t.Errorf("expected no drift warning on a clean rebuild, got: %q", stderr)
	}
}

// TestWriteOutputFilesSilentOnFirstBuild verifies a brand new path (never
// tracked in a prior manifest) never triggers the drift warning, even though
// there's nothing on disk to compare against.
func TestWriteOutputFilesSilentOnFirstBuild(t *testing.T) {
	dir := t.TempDir()
	stderr := captureStderr(t, func() {
		files := []OutputFile{{Path: "src/index.ts", Content: []byte("// v1\n")}}
		if err := WriteOutputFiles(dir, files); err != nil {
			t.Fatalf("WriteOutputFiles: %v", err)
		}
	})
	if stderr != "" {
		t.Errorf("expected no warnings on a first build, got: %q", stderr)
	}
}

// TestNormalizeOutputPathRejectsEscapes is a table-driven test covering all
// the path-shapes that must be rejected because they could escape outDir
// (absolute paths, "..", embedded "../", bare "."), plus the inputs that
// should be accepted and canonicalized to forward-slash form.
func TestNormalizeOutputPathRejectsEscapes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "double-dot", input: "..", wantErr: true},
		{name: "parent-prefix", input: "../etc/passwd", wantErr: true},
		{name: "absolute-unix", input: "/etc/passwd", wantErr: true},
		{name: "embedded-parent-resolves-up", input: "foo/../../bar", wantErr: true},
		{name: "simple-file", input: "main.go", want: "main.go"},
		{name: "nested-file", input: "src/index.ts", want: "src/index.ts"},
		{name: "self-prefix-cleaned", input: "./src/index.ts", want: "src/index.ts"},
		{name: "internal-parent-resolves-clean", input: "src/../main.go", want: "main.go"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeOutputPath(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("normalizeOutputPath(%q) = %q, nil; want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("normalizeOutputPath(%q) returned error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("normalizeOutputPath(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
