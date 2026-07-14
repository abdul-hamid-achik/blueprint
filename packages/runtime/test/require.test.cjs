const assert = require("node:assert/strict");
const test = require("node:test");

// Exercise the `require` condition in the package export map. Keeping this
// contract avoids breaking CommonJS middleware while generated services use
// the ESM entrypoint.
const { BpError, paginate } = require("@blueprint/runtime");

test("the CommonJS export exposes the public runtime API", () => {
  assert.equal(new BpError(409, "Conflict").status, 409);
  assert.equal(paginate([], 0, 1, 20).totalPages, 0);
});
