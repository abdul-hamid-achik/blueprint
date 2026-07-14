"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { readServerConfiguration } = require("../client-config");

function configuration(values = {}) {
  return {
    get(key, fallback) {
      return Object.prototype.hasOwnProperty.call(values, key) ? values[key] : fallback;
    },
  };
}

test("uses bp lsp defaults", () => {
  assert.deepEqual(readServerConfiguration(configuration()), {
    command: "bp",
    args: ["lsp"],
  });
});

test("supports a custom executable and argument list without mutating settings", () => {
  const args = ["lsp", "--stdio"];
  const result = readServerConfiguration(
    configuration({ "server.path": "  /opt/blueprint/bin/bp  ", "server.args": args }),
  );
  assert.deepEqual(result, { command: "/opt/blueprint/bin/bp", args });
  assert.notStrictEqual(result.args, args);
});

test("rejects empty or malformed executable configuration", () => {
  assert.throws(() => readServerConfiguration(configuration({ "server.path": "  " })), /non-empty string/);
  assert.throws(() => readServerConfiguration(configuration({ "server.path": ["bp"] })), /non-empty string/);
  assert.throws(() => readServerConfiguration(configuration({ "server.path": "bp\0bad" })), /NUL byte/);
});

test("rejects malformed argument configuration instead of coercing it", () => {
  assert.throws(() => readServerConfiguration(configuration({ "server.args": "lsp" })), /array of strings/);
  assert.throws(() => readServerConfiguration(configuration({ "server.args": ["lsp", 1] })), /array of strings/);
  assert.throws(() => readServerConfiguration(configuration({ "server.args": ["lsp\0bad"] })), /NUL bytes/);
});
