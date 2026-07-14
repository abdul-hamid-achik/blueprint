import assert from "node:assert/strict";
import test from "node:test";

// Import through the package's own export map so the test also catches
// mismatches between package.json and the files produced by `tsc`.
import { BpError, paginate, requireEnv } from "@blueprint/runtime";

test("BpError exposes a stable JSON response shape", () => {
  const error = new BpError(404, "Todo not found", "TODO_NOT_FOUND");

  assert.equal(error.name, "BpError");
  assert.equal(error.status, 404);
  assert.deepEqual(error.toJSON(), {
    error: "Todo not found",
    code: "TODO_NOT_FOUND",
  });
});

test("paginate reports the page metadata", () => {
  assert.deepEqual(paginate(["a", "b"], 11, 2, 5), {
    items: ["a", "b"],
    total: 11,
    page: 2,
    perPage: 5,
    totalPages: 3,
  });
});

test("requireEnv returns configured values and rejects missing ones", () => {
  const presentKey = "BLUEPRINT_RUNTIME_TEST_PRESENT";
  const missingKey = "BLUEPRINT_RUNTIME_TEST_MISSING";
  const previousPresent = process.env[presentKey];
  const previousMissing = process.env[missingKey];

  try {
    process.env[presentKey] = "configured";
    delete process.env[missingKey];

    assert.equal(requireEnv(presentKey), "configured");
    assert.throws(
      () => requireEnv(missingKey),
      (error) =>
        error instanceof BpError &&
        error.status === 500 &&
        error.code === "ENV_MISSING",
    );
  } finally {
    if (previousPresent === undefined) delete process.env[presentKey];
    else process.env[presentKey] = previousPresent;

    if (previousMissing === undefined) delete process.env[missingKey];
    else process.env[missingKey] = previousMissing;
  }
});
