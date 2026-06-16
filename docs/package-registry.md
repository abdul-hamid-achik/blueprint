# Blueprint Package Registry

> **Status: DESIGN / NOT YET IMPLEMENTED** — none of these commands ship in the current `bp` binary (v0.10.0). `bp add`, `bp list`, `bp search`, `bp update`, `bp info`, `bp remove`, and `bp package …` are **not** real commands — running them prints `Unknown command`. The `pkg:` include resolution, `blueprint.json`/`blueprint.lock` files, and the registry HTTP API described below are a proposed design (an RFC), not a working feature. Run `bp help` to see what actually exists today. This page is kept to document the intended design; treat every command and file format here as a sketch, not a tutorial.

This document describes the **proposed** Blueprint Package Registry design. It is a design document (RFC) for a feature that has not been built — see the status banner above.

## Overview

The Package Registry allows Blueprint developers to share and reuse code. Packages can contain:

- **Middleware** - Reusable authentication, logging, CORS, etc.
- **Functions** - Shared business logic
- **Pipes** - Common validation/transformation pipelines
- **Types** - Shared type definitions
- **Enums** - Shared enumeration values

## Package Structure

A package is a directory containing:

```
auth-middleware/
├── blueprint.json    # Package manifest
├── index.bp        # Main entry point
├── jwt.bp          # Additional files
└── README.md       # Documentation
```

## Manifest Format

The `blueprint.json` manifest file:

```json
{
  "name": "auth-middleware",
  "version": "1.0.0",
  "description": "JWT authentication middleware for Blueprint",
  "author": "blueprint-team",
  "license": "MIT",
  "keywords": ["auth", "jwt", "middleware"],
  "main": "index.bp",
  "exports": [
    {
      "name": "require_auth",
      "type": "middleware",
      "description": "Middleware that requires a valid JWT token"
    },
    {
      "name": "generate_token",
      "type": "fn",
      "description": "Generate a JWT token for a user"
    }
  ],
  "dependencies": {
    "jwt-utils": "^1.0.0"
  }
}
```

## Using Packages (planned)

> None of the commands or `pkg:` includes in this section work yet — they describe the intended design.

### Installing Packages

```bash
# Install latest version
bp add auth-middleware

# Install specific version
bp add auth-middleware@v1.2.3

# Install from GitHub
bp add github.com/user/blueprint-auth
```

### Using Installed Packages

The `include` keyword is real today, but only for local file paths (e.g. `include "models.bp"`). The `pkg:` scheme below is part of the proposed design and is not yet resolved by the compiler, so the following is illustrative pseudocode (it will not pass `bp check`), not a runnable snippet. In your `.bp` file it would look like:

```text
blueprint "my-api" { ... }

# Include the entire package
include "pkg:auth-middleware"

# Or include specific files
include "pkg:auth-middleware/jwt"

# Use the exported middleware
POST /api/protected {
  use require_auth
  ...
}
```

## Local Installation (planned)

Under the proposed design, packages would be installed to `blueprint_packages/` in your project:

```
my-project/
├── api.bp
├── blueprint.lock
└── blueprint_packages/
    ├── auth-middleware@1.0.0/
    │   ├── blueprint.json
    │   └── index.bp
    └── rate-limit@2.1.0/
        ├── blueprint.json
        └── index.bp
```

## Lock File (planned)

Under the proposed design, a `blueprint.lock` file would pin exact versions:

```json
{
  "version": "1.0",
  "packages": [
    {
      "name": "auth-middleware",
      "version": "1.0.0",
      "resolved": "https://registry.blueprint-lang.dev/auth-middleware/1.0.0",
      "integrity": "sha256:abc123...",
      "dependencies": {
        "jwt-utils": "^1.0.0"
      }
    }
  ]
}
```

## Planned CLI

None of the commands below exist in the current `bp` binary — they are the proposed surface for this feature. Running any of them today prints `Unknown command`. Run `bp help` for the commands that actually ship.

```bash
# Install a package
bp add <package>[@<version>]

# Remove a package
bp remove <package>

# List installed packages
bp list

# Search for packages
bp search <query>

# Update packages
bp update

# Show package info
bp info <package>
```

### Publishing Packages (planned)

```bash
# Initialize a new package
bp package init

# Validate package structure
bp package check

# Publish to registry
bp package publish
```

## Package Naming

- Use lowercase letters, numbers, and hyphens
- Must start with a letter
- Use descriptive names: `auth-middleware`, `stripe-payments`, `slack-notifications`
- Avoid generic names: `utils`, `helpers`, `common`

## Versioning

Follow [Semantic Versioning](https://semver.org/):

- `MAJOR.MINOR.PATCH`
- Breaking changes → bump MAJOR
- New features → bump MINOR
- Bug fixes → bump PATCH

## Registry API (planned)

No registry is hosted yet; there is no live endpoint at `registry.blueprint-lang.dev`. The proposed design would expose a simple HTTP API:

### Search Packages

```http
GET /api/v1/packages?query=auth
```

```json
{
  "packages": [
    {
      "name": "auth-middleware",
      "version": "1.0.0",
      "description": "JWT authentication middleware",
      "author": "blueprint-team"
    }
  ]
}
```

### Get Package

```http
GET /api/v1/packages/auth-middleware
```

```json
{
  "name": "auth-middleware",
  "versions": ["1.0.0", "0.9.0", "0.8.0"],
  "latest": "1.0.0"
}
```

### Download Package

```http
GET /api/v1/packages/auth-middleware/1.0.0/download
```

Returns a tarball of the package.

## Future Enhancements

- [ ] Private registries (self-hosted)
- [ ] Scoped packages (`@myorg/package`)
- [ ] Package signing/verification
- [ ] Dependency tree visualization
- [ ] Automatic security audits
- [ ] Package deprecation workflow
