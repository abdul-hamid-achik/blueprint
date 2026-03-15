// Package registry provides package management for Blueprint.
//
// The registry allows users to share and reuse Blueprint code through
// a simple package system. Packages can contain middleware, functions,
// types, and other reusable Blueprint constructs.
//
// Usage:
//
//	bp add auth-middleware     # Install a package
//	bp add auth-middleware@v2  # Install specific version
//	bp list                    # List installed packages
//	bp search auth             # Search for packages
//
// In your .bp file:
//
//	include "pkg:auth-middleware"  # Use an installed package
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PackageManifest describes a Blueprint package.
type PackageManifest struct {
	// Name is the package identifier (e.g., "auth-middleware")
	Name string `json:"name"`

	// Version follows semantic versioning (e.g., "1.2.3")
	Version string `json:"version"`

	// Description is a short summary of the package
	Description string `json:"description"`

	// Author is the package maintainer
	Author string `json:"author,omitempty"`

	// Keywords help with package discovery
	Keywords []string `json:"keywords,omitempty"`

	// License specifies the software license
	License string `json:"license,omitempty"`

	// Repository URL for the package source
	Repository string `json:"repository,omitempty"`

	// Main is the entry point file (default: "index.bp")
	Main string `json:"main,omitempty"`

	// Exports lists the blocks this package provides
	Exports []Export `json:"exports"`

	// Dependencies are other packages this one requires
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Export describes a public API of a package.
type Export struct {
	// Name is the identifier (e.g., "require_auth")
	Name string `json:"name"`

	// Type is the block type (middleware, fn, pipe, type, enum)
	Type string `json:"type"`

	// Description explains what this export does
	Description string `json:"description"`
}

// DefaultRegistryURL is the official Blueprint package registry.
const DefaultRegistryURL = "https://registry.blueprint-lang.dev"

// LocalRegistryPath is where packages are stored locally.
func LocalRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".blueprint", "packages")
}

// ProjectPackagesPath returns the path for project-local packages.
func ProjectPackagesPath(projectRoot string) string {
	return filepath.Join(projectRoot, "blueprint_packages")
}

// ParsePackageRef parses a package reference like "auth-middleware@v1.2.3".
// If no version is specified, "latest" is returned.
func ParsePackageRef(ref string) (name, version string, err error) {
	parts := strings.Split(ref, "@")
	name = parts[0]

	// Validate package name
	if !isValidPackageName(name) {
		return "", "", fmt.Errorf("invalid package name: %s", name)
	}

	if len(parts) == 1 {
		return name, "latest", nil
	}
	if len(parts) == 2 {
		return name, parts[1], nil
	}
	return "", "", fmt.Errorf("invalid package reference: %s", ref)
}

// isValidPackageName checks if a package name is valid.
// Valid names: lowercase letters, numbers, hyphens.
// Must start with a letter.
func isValidPackageName(name string) bool {
	if name == "" {
		return false
	}
	// Must start with lowercase letter
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	// Can contain lowercase letters, numbers, hyphens
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// PackageDir returns the directory name for an installed package.
func PackageDir(name, version string) string {
	if version == "latest" {
		return name
	}
	return fmt.Sprintf("%s@%s", name, version)
}

// RegistryIndex represents the package registry index.
type RegistryIndex struct {
	Packages []PackageSummary `json:"packages"`
	Version  string           `json:"version"`
}

// PackageSummary provides brief info about a package in the index.
type PackageSummary struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
}

// LockFile represents a project's dependency lock file.
type LockFile struct {
	Version  string          `json:"version"`
	Packages []LockedPackage `json:"packages"`
}

// LockedPackage represents a resolved package dependency.
type LockedPackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Resolved     string            `json:"resolved"`  // URL where package was fetched
	Integrity    string            `json:"integrity"` // Hash for verification
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// ReadLockFile reads the blueprint.lock file from a project.
func ReadLockFile(projectRoot string) (*LockFile, error) {
	path := filepath.Join(projectRoot, "blueprint.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty lock file
			return &LockFile{Version: "1.0", Packages: []LockedPackage{}}, nil
		}
		return nil, err
	}

	var lock LockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("invalid lock file: %w", err)
	}
	return &lock, nil
}

// WriteLockFile writes the lock file to a project.
func WriteLockFile(projectRoot string, lock *LockFile) error {
	path := filepath.Join(projectRoot, "blueprint.lock")
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ReadManifest reads a package manifest from a directory.
func ReadManifest(pkgDir string) (*PackageManifest, error) {
	path := filepath.Join(pkgDir, "blueprint.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest not found: %w", err)
	}

	var manifest PackageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	// Set default main if not specified
	if manifest.Main == "" {
		manifest.Main = "index.bp"
	}

	return &manifest, nil
}

// Manager handles package installation and management.
type Manager struct {
	// RegistryURL is the base URL for the package registry
	RegistryURL string

	// LocalPath is where packages are cached
	LocalPath string

	// ProjectPath is the current project's package directory
	ProjectPath string
}

// NewManager creates a new package manager.
func NewManager(projectRoot string) *Manager {
	return &Manager{
		RegistryURL: DefaultRegistryURL,
		LocalPath:   LocalRegistryPath(),
		ProjectPath: ProjectPackagesPath(projectRoot),
	}
}

// ListInstalled returns all packages installed in the project.
func (m *Manager) ListInstalled() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(m.ProjectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledPackage{}, nil
		}
		return nil, err
	}

	var packages []InstalledPackage
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifest, err := ReadManifest(filepath.Join(m.ProjectPath, entry.Name()))
		if err != nil {
			continue // Skip invalid packages
		}

		packages = append(packages, InstalledPackage{
			Manifest: manifest,
			Path:     filepath.Join(m.ProjectPath, entry.Name()),
		})
	}

	return packages, nil
}

// InstalledPackage represents a locally installed package.
type InstalledPackage struct {
	Manifest *PackageManifest
	Path     string
}

// ResolvePkgPath resolves a package: URI to the actual file path.
// For example, "pkg:auth-middleware" -> "blueprint_packages/auth-middleware/index.bp"
func (m *Manager) ResolvePkgPath(pkgRef string) (string, error) {
	if !strings.HasPrefix(pkgRef, "pkg:") {
		return "", fmt.Errorf("not a package reference: %s", pkgRef)
	}

	name := strings.TrimPrefix(pkgRef, "pkg:")
	name, version, err := ParsePackageRef(name)
	if err != nil {
		return "", err
	}

	dir := PackageDir(name, version)
	pkgPath := filepath.Join(m.ProjectPath, dir)

	manifest, err := ReadManifest(pkgPath)
	if err != nil {
		return "", fmt.Errorf("package not found: %s", name)
	}

	return filepath.Join(pkgPath, manifest.Main), nil
}

// ExampleManifest returns an example package manifest for documentation.
func ExampleManifest() *PackageManifest {
	return &PackageManifest{
		Name:        "auth-middleware",
		Version:     "1.0.0",
		Description: "JWT authentication middleware for Blueprint",
		Author:      "blueprint-team",
		License:     "MIT",
		Keywords:    []string{"auth", "jwt", "middleware", "security"},
		Main:        "index.bp",
		Exports: []Export{
			{
				Name:        "require_auth",
				Type:        "middleware",
				Description: "Middleware that requires a valid JWT token",
			},
			{
				Name:        "generate_token",
				Type:        "fn",
				Description: "Generate a JWT token for a user",
			},
		},
		Dependencies: map[string]string{
			"jwt-utils": "^1.0.0",
		},
	}
}
