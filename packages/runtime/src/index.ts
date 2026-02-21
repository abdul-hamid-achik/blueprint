/**
 * @blueprint/runtime — shared utilities for Blueprint-generated TypeScript projects.
 *
 * Generated projects are self-contained and do NOT require this package.
 * It is provided as an optional convenience for advanced use cases such as:
 *   - Custom middleware that extends Blueprint-generated code
 *   - Shared error handling across multiple Blueprint services
 *   - Type utilities for Blueprint model inspection
 */

// ─── Error ────────────────────────────────────────────────────────────────────

export class BpError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly code?: string,
  ) {
    super(message);
    this.name = "BpError";
  }

  toJSON() {
    return {
      error: this.message,
      ...(this.code ? { code: this.code } : {}),
    };
  }
}

// ─── Pagination ───────────────────────────────────────────────────────────────

export interface PaginatedResult<T> {
  items: T[];
  total: number;
  page: number;
  perPage: number;
  totalPages: number;
}

export function paginate<T>(
  items: T[],
  total: number,
  page: number,
  perPage: number,
): PaginatedResult<T> {
  return {
    items,
    total,
    page,
    perPage,
    totalPages: Math.ceil(total / perPage),
  };
}

// ─── Environment ──────────────────────────────────────────────────────────────

export function requireEnv(key: string): string {
  const val = process.env[key];
  if (!val) throw new BpError(500, `Missing required env var: ${key}`, "ENV_MISSING");
  return val;
}

// ─── Type utilities ───────────────────────────────────────────────────────────

/** Maps a Blueprint primitive type name to its TypeScript equivalent. */
export type BpTypeMap = {
  string: string;
  int: number;
  float: number;
  bool: boolean;
  uuid: string;
  timestamp: Date;
  json: unknown;
  money: number;
};

export type BpType = keyof BpTypeMap;
