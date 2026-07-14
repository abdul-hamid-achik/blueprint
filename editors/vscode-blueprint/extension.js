"use strict";

const { readServerConfiguration } = require("./client-config");
const vscode = require("vscode");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

async function activate(context) {
  const watcher = vscode.workspace.createFileSystemWatcher("**/*.bp");
  context.subscriptions.push(watcher);
  context.subscriptions.push(
    vscode.commands.registerCommand("blueprint.restartLanguageServer", async () => {
      await restartClient(watcher);
    }),
  );
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration("blueprint.server")) {
        await restartClient(watcher);
      }
    }),
  );

  await startClient(watcher);
}

async function startClient(watcher) {
  let server;
  try {
    server = readServerConfiguration(vscode.workspace.getConfiguration("blueprint"));
  } catch (error) {
    reportStartFailure(vscode, error);
    return;
  }

  const serverOptions = {
    command: server.command,
    args: server.args,
    transport: TransportKind.stdio,
  };
  const clientOptions = {
    documentSelector: [
      { scheme: "file", language: "blueprint" },
      { scheme: "untitled", language: "blueprint" },
    ],
    synchronize: {
      fileEvents: watcher,
    },
  };

  const nextClient = new LanguageClient(
    "blueprint",
    "Blueprint Language Server",
    serverOptions,
    clientOptions,
  );
  client = nextClient;
  try {
    await nextClient.start();
  } catch (error) {
    if (client === nextClient) {
      client = undefined;
    }
    reportStartFailure(vscode, error, server.command, server.args);
  }
}

async function restartClient(watcher) {
  await stopClient();
  await startClient(watcher);
}

async function stopClient() {
  const current = client;
  client = undefined;
  if (current) {
    await current.stop();
  }
}

function reportStartFailure(vscode, error, command, args) {
  const invocation = command ? ` (${[command, ...(args || [])].join(" ")})` : "";
  const detail = error instanceof Error ? error.message : String(error);
  void vscode.window
    .showErrorMessage(
      `Blueprint language server could not start${invocation}: ${detail}`,
      "Open Blueprint Settings",
    )
    .then((choice) => {
      if (choice === "Open Blueprint Settings") {
        void vscode.commands.executeCommand("workbench.action.openSettings", "@ext:abdul-hamid-achik.blueprint-language");
      }
    });
}

async function deactivate() {
  await stopClient();
}

module.exports = { activate, deactivate };
