# Multi-Target Code Generation

This document describes the design for multi-target code generation in Blueprint.

## Overview

Blueprint currently generates JavaScript/TypeScript for Node.js. The architecture supports multiple targets through the `codegen.Generator` interface.

## Planned Targets

| Target | Status | Stack |
|--------|--------|-------|
| JavaScript/Node.js | ✅ Complete | Hono + Drizzle + Zod |
| Python | 🚧 Planned | FastAPI + SQLAlchemy + Pydantic |
| Go | 🚧 Planned | Chi/Gin + sqlc + validator |
| Deno | 🚧 Planned | Oak + Deno KV |

## Generator Interface

All generators implement:

```go
type Generator interface {
    Generate(file *ast.File, outDir string) error
}
```

## Target Selection

Users select the target in the blueprint declaration:

```bp
blueprint "my-api" {
  version "1.0.0"
  port    3000
  runtime python  # or: node, go, deno
}
```

## Implementation Structure

```
internal/codegen/
├── codegen.go          # Generator interface
├── js/                 # JavaScript/TypeScript generator
│   ├── generator.go
│   ├── routes.go
│   └── helpers.go
├── python/             # Python generator (planned)
│   ├── generator.go
│   ├── routes.go
│   └── models.go
└── go/                 # Go generator (planned)
    ├── generator.go
    ├── routes.go
    └── models.go
```

## Python Target Design

### Generated Stack

- **Framework**: FastAPI
- **ORM**: SQLAlchemy 2.0
- **Validation**: Pydantic v2
- **Database**: PostgreSQL via asyncpg
- **Migrations**: Alembic

### Example Output

**main.py**:
```python
from fastapi import FastAPI
from contextlib import asynccontextmanager

@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup
    await db.connect()
    yield
    # Shutdown
    await db.disconnect()

app = FastAPI(lifespan=lifespan)

@app.get("/health")
async def health():
    return {"status": "ok"}

app.include_router(items_router)
```

**routes/items.py**:
```python
from fastapi import APIRouter, Depends
from sqlalchemy import select
from pydantic import BaseModel

router = APIRouter()

class CreateItemRequest(BaseModel):
    name: str
    price: float

@router.post("/api/items", status_code=201)
async def create_item(req: CreateItemRequest, db: AsyncSession = Depends(get_db)):
    item = Item(name=req.name, price=req.price)
    db.add(item)
    await db.commit()
    await db.refresh(item)
    return {"id": item.id, "name": item.name}
```

**models.py**:
```python
from sqlalchemy import Column, Integer, String, Float, DateTime
from sqlalchemy.orm import declarative_base
from sqlalchemy.sql import func

Base = declarative_base()

class Item(Base):
    __tablename__ = "items"
    
    id = Column(Integer, primary_key=True)
    name = Column(String, nullable=False)
    price = Column(Float, nullable=False)
    created_at = Column(DateTime(timezone=True), server_default=func.now())
```

## Go Target Design

### Generated Stack

- **Framework**: Chi or Gin
- **Database**: sqlc for type-safe SQL
- **Validation**: go-playground/validator
- **Migrations**: golang-migrate

### Example Output

**main.go**:
```go
package main

import (
    "net/http"
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
)

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    
    r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(`{"status":"ok"}`))
    })
    
    r.Route("/api", func(r chi.Router) {
        r.Route("/items", itemRoutes)
    })
    
    http.ListenAndServe(":3000", r)
}
```

## Adding a New Target

To add a new code generation target:

1. Create `internal/codegen/<target>/` directory
2. Implement the `Generator` interface
3. Create target-specific helpers (naming conventions, type mappings)
4. Add runtime enum to checker
5. Update CLI to accept the new runtime value

### Type Mapping

Each target maps Blueprint types to native types:

| Blueprint | JavaScript | Python | Go |
|-----------|------------|--------|-----|
| `string` | `string` | `str` | `string` |
| `int` | `number` | `int` | `int64` |
| `float` | `number` | `float` | `float64` |
| `bool` | `boolean` | `bool` | `bool` |
| `uuid` | `string` | `UUID` | `uuid.UUID` |
| `timestamp` | `Date` | `datetime` | `time.Time` |
| `json` | `any` | `dict` | `json.RawMessage` |
| `money` | `Decimal` | `Decimal` | `decimal.Decimal` |

### Naming Conventions

| Construct | JavaScript | Python | Go |
|-----------|------------|--------|-----|
| Variables | `camelCase` | `snake_case` | `camelCase` |
| Functions | `camelCase` | `snake_case` | `CamelCase` |
| Types | `PascalCase` | `PascalCase` | `PascalCase` |
| Files | `kebab-case.ts` | `snake_case.py` | `snake_case.go` |
| Constants | `SCREAMING_SNAKE` | `SCREAMING_SNAKE` | `ScreamingSnake` |

## Intermediate Representation (IR)

Future enhancement: Add an IR layer between AST and code generation.

```
.bp source
    ↓
Parser → AST
    ↓
Checker → Validated AST
    ↓
Lower → IR (target-independent)
    ↓
Codegen → Target code
```

Benefits:
- Easier to add new targets
- Target-independent optimizations
- Consistent semantics across targets

## Implementation Priority

1. **Python/FastAPI** - High demand for ML/data API backends
2. **Go** - Performance-critical services
3. **Deno** - Modern JavaScript runtime
