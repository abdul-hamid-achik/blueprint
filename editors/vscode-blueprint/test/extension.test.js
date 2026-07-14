"use strict";

const assert = require("node:assert/strict");
const Module = require("node:module");
const test = require("node:test");

async function loadExtensionWithMocks(options = {}) {
  const state = {
    clients: [],
    commands: new Map(),
    errors: [],
    executedCommands: [],
    watchers: [],
  };
  const values = options.configuration || {};
  const watcher = { dispose() {} };
  const vscode = {
    workspace: {
      createFileSystemWatcher(pattern) {
        state.watchers.push(pattern);
        return watcher;
      },
      getConfiguration(section) {
        assert.equal(section, "blueprint");
        return {
          get(key, fallback) {
            return Object.prototype.hasOwnProperty.call(values, key) ? values[key] : fallback;
          },
        };
      },
      onDidChangeConfiguration(listener) {
        state.configurationListener = listener;
        return { dispose() {} };
      },
    },
    commands: {
      registerCommand(name, callback) {
        state.commands.set(name, callback);
        return { dispose() {} };
      },
      async executeCommand(...args) {
        state.executedCommands.push(args);
      },
    },
    window: {
      async showErrorMessage(...args) {
        state.errors.push(args);
        return options.errorChoice;
      },
    },
  };

  class FakeLanguageClient {
    constructor(...args) {
      this.constructorArgs = args;
      this.startCalls = 0;
      this.stopCalls = 0;
      state.clients.push(this);
    }

    async start() {
      this.startCalls++;
      if (options.startError) {
        throw options.startError;
      }
    }

    async stop() {
      this.stopCalls++;
    }
  }

  const originalLoad = Module._load;
  Module._load = function load(request, parent, isMain) {
    if (request === "vscode") {
      return vscode;
    }
    if (request === "vscode-languageclient/node") {
      return { LanguageClient: FakeLanguageClient, TransportKind: { stdio: "stdio" } };
    }
    return originalLoad.call(this, request, parent, isMain);
  };
  const extensionPath = require.resolve("../extension");
  delete require.cache[extensionPath];
  let extension;
  try {
    extension = require(extensionPath);
  } finally {
    Module._load = originalLoad;
  }

  return {
    extension,
    state,
    context: { subscriptions: [] },
  };
}

test("activation starts bp lsp over stdio and restart replaces the client", async () => {
  const { extension, state, context } = await loadExtensionWithMocks();
  await extension.activate(context);

  assert.deepEqual(state.watchers, ["**/*.bp"]);
  assert.equal(state.clients.length, 1);
  const [id, name, serverOptions, clientOptions] = state.clients[0].constructorArgs;
  assert.equal(id, "blueprint");
  assert.equal(name, "Blueprint Language Server");
  assert.deepEqual(serverOptions, { command: "bp", args: ["lsp"], transport: "stdio" });
  assert.deepEqual(clientOptions.documentSelector, [
    { scheme: "file", language: "blueprint" },
    { scheme: "untitled", language: "blueprint" },
  ]);
  assert.equal(state.clients[0].startCalls, 1);
  assert.equal(clientOptions.synchronize.fileEvents.dispose instanceof Function, true);

  const restart = state.commands.get("blueprint.restartLanguageServer");
  assert.equal(typeof restart, "function");
  await restart();
  assert.equal(state.clients[0].stopCalls, 1);
  assert.equal(state.clients.length, 2);
  assert.equal(state.clients[1].startCalls, 1);

  await extension.deactivate();
  assert.equal(state.clients[1].stopCalls, 1);
});

test("custom server settings are passed exactly to the language client", async () => {
  const { extension, state, context } = await loadExtensionWithMocks({
    configuration: {
      "server.path": "/opt/blueprint/bp",
      "server.args": ["lsp", "--stdio"],
    },
  });
  await extension.activate(context);
  const serverOptions = state.clients[0].constructorArgs[2];
  assert.deepEqual(serverOptions, {
    command: "/opt/blueprint/bp",
    args: ["lsp", "--stdio"],
    transport: "stdio",
  });
  await extension.deactivate();
});

test("startup failures are reported with an actionable settings link", async () => {
  const { extension, state, context } = await loadExtensionWithMocks({
    startError: new Error("spawn bp ENOENT"),
    errorChoice: "Open Blueprint Settings",
  });
  await extension.activate(context);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(state.errors.length, 1);
  assert.match(state.errors[0][0], /bp lsp/);
  assert.match(state.errors[0][0], /spawn bp ENOENT/);
  assert.deepEqual(state.executedCommands, [
    ["workbench.action.openSettings", "@ext:abdul-hamid-achik.blueprint-language"],
  ]);
  await extension.deactivate();
});

test("invalid configuration fails closed before constructing a client", async () => {
  const { extension, state, context } = await loadExtensionWithMocks({
    configuration: { "server.args": ["lsp", 42] },
  });
  await extension.activate(context);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(state.clients.length, 0);
  assert.equal(state.errors.length, 1);
  assert.match(state.errors[0][0], /array of strings/);
  await extension.deactivate();
});
