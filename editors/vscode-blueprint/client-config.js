"use strict";

function readServerConfiguration(configuration) {
  if (!configuration || typeof configuration.get !== "function") {
    throw new TypeError("Blueprint server configuration is unavailable");
  }

  const configuredPath = configuration.get("server.path", "bp");
  if (typeof configuredPath !== "string" || configuredPath.trim() === "") {
    throw new TypeError("blueprint.server.path must be a non-empty string");
  }
  if (configuredPath.includes("\0")) {
    throw new TypeError("blueprint.server.path must not contain a NUL byte");
  }

  const configuredArgs = configuration.get("server.args", ["lsp"]);
  if (!Array.isArray(configuredArgs) || configuredArgs.some((arg) => typeof arg !== "string" || arg.includes("\0"))) {
    throw new TypeError("blueprint.server.args must be an array of strings without NUL bytes");
  }

  return {
    command: configuredPath.trim(),
    args: [...configuredArgs],
  };
}

module.exports = { readServerConfiguration };
