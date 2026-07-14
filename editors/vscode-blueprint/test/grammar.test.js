"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const grammarPath = path.join(__dirname, "..", "syntaxes", "bp.tmLanguage.json");

test("TextMate grammar highlights computed as a declaration keyword", () => {
  const grammar = JSON.parse(fs.readFileSync(grammarPath, "utf8"));
  const declarations = grammar.repository?.declarations;

  assert.equal(declarations?.name, "keyword.declaration.blueprint");
  const declarationPattern = new RegExp(declarations.match);
  assert.equal(declarationPattern.test("computed"), true);
  assert.equal(declarationPattern.test("recomputed"), false);
});
