# `@blueprint/runtime`

Small, optional utilities for extending Blueprint-generated TypeScript
services. Generated projects remain self-contained and do not depend on this
package.

```sh
npm install @blueprint/runtime
```

```ts
import { BpError, paginate, requireEnv } from "@blueprint/runtime"

const databaseUrl = requireEnv("DATABASE_URL")
const page = paginate(rows, total, 1, 20)
throw new BpError(404, "Todo not found", "TODO_NOT_FOUND")
```

The package also exports `BpTypeMap`, `BpType`, and `PaginatedResult<T>` for
custom middleware and integrations. ESM imports and CommonJS `require()` are
both supported on Node.js 18 or newer.
