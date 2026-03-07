# Blueprint Package Registry

This document describes the Blueprint Package Registry design.

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

## Using Packages

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

In your `.bp` file:

```bp
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

## Local Installation

Packages are installed to `blueprint_packages/` in your project:

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

## Lock File

The `blueprint.lock` file pins exact versions:

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

## Registry Commands

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

## Publishing Packages

(Planned for future)

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

## Registry API

The registry exposes a simple HTTP API:

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
