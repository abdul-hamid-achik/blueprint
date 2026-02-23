// Package js generates JavaScript/TypeScript code from Blueprint AST.
package js

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Generator produces a Node.js project from a Blueprint AST.
type Generator struct {
	sourceFile  string
	file        *ast.File
	middlewares map[string]*ast.Middleware // name -> middleware definition
	// Lookup maps for declared names (built once in generateAll).
	declaredFns       map[string]bool
	declaredPipes     map[string]bool
	declaredModels    map[string]bool
	declaredEnums     map[string]bool
	structEnums       map[string]bool // enums with struct-body variants (e.g., Plan)
	declaredExternals map[string]bool // normalized camelCase external service names
	hasStorage        bool
}

// New creates a new JS generator.
func New() *Generator {
	return &Generator{}
}

// emitCtx carries context for arrow statement code generation.
type emitCtx struct {
	kind        string            // "endpoint", "function", "middleware"
	method      string            // HTTP method for endpoints (e.g., "GET", "POST")
	path        string            // URL path for endpoints (e.g., "/api/todos/:id")
	ctxVars     map[string]bool   // identifiers injected via middleware (e.g., "auth" -> c.get('auth'))
	boundVars   map[string]string // data-op binding: model name -> bound variable (e.g., "job" -> "job")
	declared    map[string]bool   // variables already declared in current scope
	varModels   map[string]string // reverse of boundVars: variable name -> model name (e.g., "old" -> "job")
	singleVars  map[string]bool   // variables bound from fetch (single record, not a collection)
	asyncFns    map[string]bool   // function/pipe names that should be awaited
	structEnums map[string]bool   // enum names that have struct-body variants (bracket access → <Name>Config)
}

// Generate implements codegen.Generator.
func (g *Generator) Generate(file *ast.File, outDir string) error {
	g.file = file
	g.sourceFile = file.Loc.File
	if g.sourceFile == "" {
		g.sourceFile = "main.bp"
	}

	files, err := g.generateAll()
	if err != nil {
		return fmt.Errorf("codegen: %w", err)
	}

	for _, f := range files {
		path := filepath.Join(outDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, f.Content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// buildAsyncFns returns the set of camelCase function names that should be awaited in generated code.
func (g *Generator) buildAsyncFns() map[string]bool {
	fns := map[string]bool{
		"upload":        true,
		"deleteS3Object": true,
	}
	for name := range g.declaredFns {
		fns[toCamelCase(name)] = true
	}
	for name := range g.declaredPipes {
		fns[toCamelCase(name)] = true
	}
	return fns
}

// generateAll produces all output files from the AST.
func (g *Generator) generateAll() ([]codegen.OutputFile, error) {
	var files []codegen.OutputFile

	// Classify blocks
	var (
		secrets     []*ast.Secret
		envs        []*ast.Env
		types       []*ast.TypeDecl
		aliases     []*ast.Alias
		enums       []*ast.Enum
		models      []*ast.Model
		fns         []*ast.Fn
		pipes       []*ast.Pipe
		middlewares []*ast.Middleware
		endpoints   []*ast.Endpoint
		streams     []*ast.StreamEndpoint
		ws          []*ast.WsEndpoint
		workers     []*ast.Worker
		schedules   []*ast.Schedule
		externals   []*ast.External
		subscribes  []*ast.Subscribe
		tests       []*ast.Test
		fixtures    []*ast.Fixture
	)

	g.middlewares = make(map[string]*ast.Middleware)

	for _, block := range g.file.Blocks {
		switch b := block.(type) {
		case *ast.Secret:
			secrets = append(secrets, b)
		case *ast.Env:
			envs = append(envs, b)
		case *ast.TypeDecl:
			types = append(types, b)
		case *ast.Alias:
			aliases = append(aliases, b)
		case *ast.Enum:
			enums = append(enums, b)
		case *ast.Model:
			models = append(models, b)
		case *ast.Fn:
			fns = append(fns, b)
		case *ast.Pipe:
			pipes = append(pipes, b)
		case *ast.Middleware:
			middlewares = append(middlewares, b)
			g.middlewares[b.Name] = b
		case *ast.Endpoint:
			endpoints = append(endpoints, b)
		case *ast.StreamEndpoint:
			streams = append(streams, b)
		case *ast.WsEndpoint:
			ws = append(ws, b)
		case *ast.Worker:
			workers = append(workers, b)
		case *ast.Schedule:
			schedules = append(schedules, b)
		case *ast.External:
			externals = append(externals, b)
		case *ast.Subscribe:
			subscribes = append(subscribes, b)
		case *ast.Test:
			tests = append(tests, b)
		case *ast.Fixture:
			fixtures = append(fixtures, b)
		}
	}

	// Build lookup maps for import resolution
	g.declaredFns = make(map[string]bool)
	for _, fn := range fns {
		g.declaredFns[fn.Name] = true
	}
	g.declaredPipes = make(map[string]bool)
	for _, p := range pipes {
		g.declaredPipes[p.Name] = true
	}
	g.declaredModels = make(map[string]bool)
	for _, m := range models {
		g.declaredModels[m.Name] = true
	}
	g.declaredEnums = make(map[string]bool)
	g.structEnums = make(map[string]bool)
	for _, e := range enums {
		g.declaredEnums[e.Name] = true
		for _, v := range e.Variants {
			if v.Body != nil && len(v.Body.Entries) > 0 {
				g.structEnums[e.Name] = true
				break
			}
		}
	}
	g.declaredExternals = make(map[string]bool)
	for _, ext := range externals {
		g.declaredExternals[normalizeServiceName(ext.Name)] = true
	}

	bp := g.file.Blueprint
	hasDB := blueprintEntry(bp, "database") != ""
	hasCache := blueprintEntry(bp, "cache") != ""
	hasStorage := blueprintEntry(bp, "storage") != ""
	g.hasStorage = hasStorage

	// package.json
	files = append(files, g.genPackageJSON(bp, hasDB, hasCache, hasStorage, len(workers)+len(schedules) > 0, len(endpoints) > 0))

	// tsconfig.json
	files = append(files, g.genTSConfig())

	// .env.example
	files = append(files, g.genEnvExample(secrets, envs))

	// src/index.ts — entrypoint
	files = append(files, g.genIndex(bp, endpoints, streams, ws, middlewares, subscribes))

	// src/lib/env.ts — env validation
	files = append(files, g.genEnvTS(secrets, envs))

	// src/lib/errors.ts
	files = append(files, g.genErrors())

	// src/lib/db.ts (if database)
	if hasDB {
		files = append(files, g.genDB(bp))
	}

	// src/lib/storage.ts (if storage)
	if hasStorage {
		files = append(files, g.genStorage())
	}

	// src/lib/cache.ts (if cache)
	if hasCache {
		files = append(files, g.genCache())
	}

	// src/types.ts
	if len(types) > 0 || len(aliases) > 0 || len(enums) > 0 {
		files = append(files, g.genTypes(types, aliases, enums))
	}

	// src/models/schema.ts
	if len(models) > 0 {
		files = append(files, g.genSchema(models, enums))
	}

	// src/validation/schemas.ts
	if len(endpoints) > 0 {
		files = append(files, g.genValidation(endpoints))
	}

	// src/routes/<resource>.ts — group endpoints by resource
	routeGroups := make(map[string][]*ast.Endpoint)
	for _, ep := range endpoints {
		res := extractResource(ep.Path)
		routeGroups[res] = append(routeGroups[res], ep)
	}
	for res, eps := range routeGroups {
		files = append(files, g.genRoute(res, eps, hasDB))
	}

	// src/functions/<name>.ts
	for _, fn := range fns {
		files = append(files, g.genFunction(fn)...)

	}

	// src/pipes/<name>.ts
	for _, p := range pipes {
		files = append(files, g.genPipe(p))
	}

	// src/middleware/<name>.ts
	for _, mw := range middlewares {
		files = append(files, g.genMiddleware(mw))
	}

	// src/workers/<name>.ts
	for _, w := range workers {
		files = append(files, g.genWorker(w))
	}

	// src/schedules/<name>.ts
	for _, s := range schedules {
		files = append(files, g.genSchedule(s))
	}

	// src/routes/<resource>[-stream].ts — stream endpoints grouped by resource
	// Use "-stream" suffix when the resource name collides with a REST route file.
	streamGroups := make(map[string][]*ast.StreamEndpoint)
	for _, se := range streams {
		res := extractResource(se.Path)
		streamGroups[res] = append(streamGroups[res], se)
	}
	for res, ses := range streamGroups {
		fileKey := res
		if _, conflict := routeGroups[res]; conflict {
			fileKey = res + "-stream"
		}
		files = append(files, g.genStreamRoute(res, fileKey, ses))
	}

	// src/routes/<resource>[-ws].ts — ws endpoints grouped by resource
	// Use "-ws" suffix when the resource name collides with a REST or stream route file.
	wsGroups := make(map[string][]*ast.WsEndpoint)
	for _, we := range ws {
		res := extractResource(we.Path)
		wsGroups[res] = append(wsGroups[res], we)
	}
	for res, wes := range wsGroups {
		fileKey := res
		_, restConflict := routeGroups[res]
		_, streamConflict := streamGroups[res]
		if restConflict || streamConflict {
			fileKey = res + "-ws"
		}
		files = append(files, g.genWsRoute(res, fileKey, wes))
	}

	// src/subscriptions/<name>.ts + src/lib/events.ts
	if len(subscribes) > 0 {
		files = append(files, g.genEventsLib())
		for _, sub := range subscribes {
			files = append(files, g.genSubscribe(sub))
		}
	}

	// src/lib/external.ts
	if len(externals) > 0 {
		files = append(files, g.genExternal(externals))
	}

	// Dockerfile
	files = append(files, g.genDockerfile())

	// test/<name>.test.ts
	for _, t := range tests {
		files = append(files, g.genTest(t, fixtures))
	}

	return files, nil
}

// --- Static files ---

func (g *Generator) genPackageJSON(bp *ast.Blueprint, hasDB, hasCache, hasStorage, hasQueue, hasEndpoints bool) codegen.OutputFile {
	name := bp.Name
	port := blueprintEntryInt(bp, "port")
	if port == 0 {
		port = 3000
	}

	deps := map[string]string{
		"hono":              "^4.6.0",
		"@hono/node-server": "^1.13.0",
		"zod":               "^3.23.0",
		"dotenv":            "^16.4.0",
	}
	if hasEndpoints {
		deps["@hono/zod-validator"] = "^0.4.2"
	}
	if hasDB {
		deps["drizzle-orm"] = "^0.36.0"
		deps["pg"] = "^8.13.0"
	}
	if hasCache {
		deps["redis"] = "^4.7.0"
	}
	if hasStorage {
		deps["@aws-sdk/client-s3"] = "^3.700.0"
	}
	if hasQueue {
		deps["bullmq"] = "^5.30.0"
	}

	devDeps := map[string]string{
		"typescript":  "^5.7.0",
		"tsx":         "^4.19.0",
		"@types/node": "^22.0.0",
		"@types/pg":   "^8.11.0",
		"vitest":      "^2.1.0",
	}
	if hasDB {
		devDeps["drizzle-kit"] = "^0.28.0"
	}

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(fmt.Sprintf(`  "name": "%s",`+"\n", name))
	b.WriteString(`  "version": "0.1.0",` + "\n")
	b.WriteString(`  "private": true,` + "\n")
	b.WriteString(`  "type": "module",` + "\n")
	b.WriteString(`  "scripts": {` + "\n")
	b.WriteString(fmt.Sprintf(`    "start": "tsx src/index.ts",`+"\n"))
	b.WriteString(fmt.Sprintf(`    "dev": "tsx watch src/index.ts",`+"\n"))
	b.WriteString(`    "build": "tsc",` + "\n")
	b.WriteString(`    "test": "vitest run",` + "\n")
	b.WriteString(`    "test:watch": "vitest"` + "\n")
	b.WriteString("  },\n")

	// Dependencies — sorted for deterministic output
	b.WriteString(`  "dependencies": {` + "\n")
	depKeys := sortedKeys(deps)
	for i, k := range depKeys {
		comma := ","
		if i == len(depKeys)-1 {
			comma = ""
		}
		b.WriteString(fmt.Sprintf(`    "%s": "%s"%s`+"\n", k, deps[k], comma))
	}
	b.WriteString("  },\n")

	// Dev dependencies — sorted for deterministic output
	b.WriteString(`  "devDependencies": {` + "\n")
	devKeys := sortedKeys(devDeps)
	for i, k := range devKeys {
		comma := ","
		if i == len(devKeys)-1 {
			comma = ""
		}
		b.WriteString(fmt.Sprintf(`    "%s": "%s"%s`+"\n", k, devDeps[k], comma))
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")

	return codegen.OutputFile{Path: "package.json", Content: []byte(b.String())}
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (g *Generator) genTSConfig() codegen.OutputFile {
	content := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "nodenext",
    "esModuleInterop": true,
    "strict": true,
    "outDir": "dist",
    "rootDir": "src",
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true
  },
  "include": ["src/**/*.ts"],
  "exclude": ["node_modules", "dist", "test"]
}
`
	return codegen.OutputFile{Path: "tsconfig.json", Content: []byte(content)}
}

func (g *Generator) genEnvExample(secrets []*ast.Secret, envs []*ast.Env) codegen.OutputFile {
	var b strings.Builder
	b.WriteString("# Generated by Blueprint — fill in your values\n\n")
	for _, s := range secrets {
		b.WriteString(fmt.Sprintf("%s=\n", s.Name))
	}
	if len(secrets) > 0 && len(envs) > 0 {
		b.WriteString("\n")
	}
	for _, e := range envs {
		val := exprToString(e.Value)
		b.WriteString(fmt.Sprintf("%s=%s\n", e.Name, val))
	}
	return codegen.OutputFile{Path: ".env.example", Content: []byte(b.String())}
}

func (g *Generator) genEnvTS(secrets []*ast.Secret, envs []*ast.Env) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { z } from 'zod';\nimport 'dotenv/config';\n\n")
	b.WriteString("const envSchema = z.object({\n")
	for _, s := range secrets {
		req := ".optional()"
		if s.Required {
			req = ""
		}
		b.WriteString(fmt.Sprintf("  %s: z.string()%s,\n", s.Name, req))
	}
	for _, e := range envs {
		zodType := "z.string()"
		defaultVal := exprToJS(e.Value)
		switch e.Value.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.SizeLit:
			zodType = "z.coerce.number()"
		case *ast.ListExpr:
			// List env vars: store as JSON string, parse on load
			zodType = "z.string().transform((v: string) => v.split(','))"
			// Convert list to comma-separated string default
			list := e.Value.(*ast.ListExpr)
			parts := make([]string, len(list.Elements))
			for i, el := range list.Elements {
				parts[i] = exprToString(el)
			}
			defaultVal = fmt.Sprintf(`"%s"`, strings.Join(parts, ","))
		case *ast.BinaryExpr:
			// Arithmetic expressions in env defaults are numeric
			zodType = "z.coerce.number()"
		case *ast.StringLit:
			// String default needs to be quoted
			defaultVal = fmt.Sprintf(`"%s"`, exprToString(e.Value))
		}
		b.WriteString(fmt.Sprintf("  %s: %s.default(%s),\n", e.Name, zodType, defaultVal))
	}
	b.WriteString("});\n\n")
	b.WriteString("export const env = envSchema.parse(process.env);\n")

	return codegen.OutputFile{Path: "src/lib/env.ts", Content: []byte(b.String())}
}

func (g *Generator) genErrors() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`export class BpError extends Error {
  constructor(
    public statusCode: number,
    message: string,
  ) {
    super(message);
    this.name = 'BpError';
  }
}

export class ValidationError extends BpError {
  constructor(message: string) {
    super(400, message);
    this.name = 'ValidationError';
  }
}

export class AuthError extends BpError {
  constructor(message: string) {
    super(401, message);
    this.name = 'AuthError';
  }
}

export class NotFoundError extends BpError {
  constructor(message: string) {
    super(404, message);
    this.name = 'NotFoundError';
  }
}
`)
	return codegen.OutputFile{Path: "src/lib/errors.ts", Content: []byte(b.String())}
}

func (g *Generator) genDB(bp *ast.Blueprint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import { drizzle } from 'drizzle-orm/node-postgres';
import pg from 'pg';
import { env } from './env.js';
import * as schema from '../models/schema.js';

const pool = new pg.Pool({
  connectionString: env.DATABASE_URL,
});

export const db = drizzle(pool, { schema });
`)
	return codegen.OutputFile{Path: "src/lib/db.ts", Content: []byte(b.String())}
}

func (g *Generator) genStorage() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import { S3Client, PutObjectCommand, GetObjectCommand, DeleteObjectCommand } from '@aws-sdk/client-s3';
import { env } from './env.js';

const s3 = new S3Client({});

export async function upload(file: Buffer | ReadableStream, bucket: string, key?: string): Promise<{ url: string; key: string }> {
  const fileKey = key ?? crypto.randomUUID();
  await s3.send(new PutObjectCommand({ Bucket: bucket, Key: fileKey, Body: file as any }));
  return { url: ` + "`s3://${bucket}/${fileKey}`" + `, key: fileKey };
}

export async function download(bucket: string, key: string): Promise<ReadableStream> {
  const res = await s3.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
  return res.Body as any;
}

export async function deleteObject(bucket: string, key: string): Promise<void> {
  await s3.send(new DeleteObjectCommand({ Bucket: bucket, Key: key }));
}
`)
	return codegen.OutputFile{Path: "src/lib/storage.ts", Content: []byte(b.String())}
}

func (g *Generator) genCache() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import { createClient } from 'redis';
import { env } from './env.js';

export const redis = createClient({ url: env.REDIS_URL });
redis.on('error', (err) => console.error('Redis error:', err));

export async function connectCache(): Promise<void> {
  if (!redis.isOpen) await redis.connect();
}

export async function cached<T>(key: string, ttlSeconds: number, fn: () => Promise<T>): Promise<T> {
  await connectCache();
  const hit = await redis.get(key);
  if (hit) return JSON.parse(hit) as T;
  const result = await fn();
  await redis.setEx(key, ttlSeconds, JSON.stringify(result));
  return result;
}
`)
	return codegen.OutputFile{Path: "src/lib/cache.ts", Content: []byte(b.String())}
}

// --- Types ---

func (g *Generator) genTypes(types []*ast.TypeDecl, aliases []*ast.Alias, enums []*ast.Enum) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	for _, e := range enums {
		name := e.Name
		isStruct := g.structEnums[e.Name]
		if isStruct {
			// Struct enum: generate plain string enum + separate config object
			b.WriteString(fmt.Sprintf("export const %s = {\n", name))
			for _, v := range e.Variants {
				b.WriteString(fmt.Sprintf("  %s: '%s',\n", v.Name, v.Name))
			}
			b.WriteString("} as const;\n")
			b.WriteString(fmt.Sprintf("export type %s = keyof typeof %s;\n\n", name, name))
			// Config object with struct field data
			b.WriteString(fmt.Sprintf("export const %sConfig: Record<string, any> = {\n", name))
			for _, v := range e.Variants {
				if v.Body != nil && len(v.Body.Entries) > 0 {
					parts := make([]string, len(v.Body.Entries))
					for i, kv := range v.Body.Entries {
						parts[i] = fmt.Sprintf("%s: %s", toCamelCase(kv.Key), exprToJS(kv.Value))
					}
					b.WriteString(fmt.Sprintf("  %s: { %s },\n", v.Name, strings.Join(parts, ", ")))
				} else {
					b.WriteString(fmt.Sprintf("  %s: {},\n", v.Name))
				}
			}
			b.WriteString("};\n\n")
		} else {
			b.WriteString(fmt.Sprintf("export const %s = {\n", name))
			for _, v := range e.Variants {
				b.WriteString(fmt.Sprintf("  %s: '%s',\n", v.Name, v.Name))
			}
			b.WriteString("} as const;\n")
			b.WriteString(fmt.Sprintf("export type %s = keyof typeof %s;\n\n", name, name))
		}
	}

	for _, a := range aliases {
		b.WriteString(fmt.Sprintf("export type %s = %s;\n\n", a.Name, typeToTS(a.Type)))
	}

	for _, t := range types {
		b.WriteString(fmt.Sprintf("export interface %s {\n", toPascalCase(t.Name)))
		for _, f := range t.Fields {
			opt := ""
			for _, c := range f.Constraints {
				if c.Kind == "optional" {
					opt = "?"
					break
				}
			}
			b.WriteString(fmt.Sprintf("  %s%s: %s;\n", toCamelCase(f.Name), opt, typeToTS(f.Type)))
		}
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/types.ts", Content: []byte(b.String())}
}

// --- Models / Schema ---

func (g *Generator) genSchema(models []*ast.Model, enums []*ast.Enum) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { pgTable, pgEnum, text, integer, real, boolean, uuid, timestamp, jsonb } from 'drizzle-orm/pg-core';\n\n")

	// Build enum name lookup for column type matching
	enumNames := make(map[string]string) // lower(name) -> varName
	for _, e := range enums {
		varName := toCamelCase(e.Name) + "Enum"
		variants := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = fmt.Sprintf("'%s'", v.Name)
		}
		b.WriteString(fmt.Sprintf("export const %s = pgEnum('%s', [%s]);\n\n",
			varName, strings.ToLower(e.Name), strings.Join(variants, ", ")))
		enumNames[strings.ToLower(e.Name)] = varName
	}

	// Emit each model as a pgTable
	for _, m := range models {
		tableName := pluralize(m.Name)
		b.WriteString(fmt.Sprintf("export const %s = pgTable('%s', {\n", toCamelCase(m.Name), tableName))

		for _, f := range m.Fields {
			colName := toCamelCase(f.Name)

			// Check for named type matching a declared enum
			if nt, ok := f.Type.(*ast.NamedType); ok {
				if enumVar, exists := enumNames[strings.ToLower(nt.Name)]; exists {
					b.WriteString(fmt.Sprintf("  %s: %s('%s')", colName, enumVar, f.Name))
					g.writeFieldConstraints(&b, f)
					b.WriteString(",\n")
					continue
				}
			}

			// Check for inline enum — generate a pgEnum for it
			if ie, ok := f.Type.(*ast.EnumInline); ok {
				inlineEnumVar := toCamelCase(m.Name) + toPascalCase(f.Name) + "Enum"
				variants := make([]string, len(ie.Variants))
				for i, v := range ie.Variants {
					variants[i] = fmt.Sprintf("'%s'", v)
				}
				// Insert the enum declaration before this table (will be above in output)
				// For simplicity, use text with a check comment for now
				b.WriteString(fmt.Sprintf("  %s: text('%s')", colName, f.Name))
				_ = inlineEnumVar
				g.writeFieldConstraints(&b, f)
				b.WriteString(",\n")
				continue
			}

			colType := typeToDrizzle(f.Type)
			b.WriteString(fmt.Sprintf("  %s: %s('%s')", colName, colType, f.Name))
			g.writeFieldConstraints(&b, f)
			b.WriteString(",\n")
		}
		b.WriteString("});\n\n")

		// Emit TypeScript type from model
		b.WriteString(fmt.Sprintf("export type %s = typeof %s.$inferSelect;\n", toPascalCase(m.Name), toCamelCase(m.Name)))
		b.WriteString(fmt.Sprintf("export type New%s = typeof %s.$inferInsert;\n\n", toPascalCase(m.Name), toCamelCase(m.Name)))
	}

	return codegen.OutputFile{Path: "src/models/schema.ts", Content: []byte(b.String())}
}

// writeFieldConstraints writes Drizzle column constraints for a field.
func (g *Generator) writeFieldConstraints(b *strings.Builder, f *ast.Field) {
	// Check if this is a uuid primary key — add .defaultRandom()
	isUUIDPrimary := false
	if pt, ok := f.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
		for _, c := range f.Constraints {
			if c.Kind == "primary" {
				isUUIDPrimary = true
				break
			}
		}
	}

	for _, c := range f.Constraints {
		switch c.Kind {
		case "primary":
			if isUUIDPrimary {
				b.WriteString(".defaultRandom().primaryKey()")
			} else {
				b.WriteString(".primaryKey()")
			}
		case "unique":
			b.WriteString(".unique()")
		case "default":
			if _, ok := c.Value.(*ast.NowLit); ok {
				b.WriteString(".defaultNow()")
			} else {
				val := exprToJS(c.Value)
				b.WriteString(fmt.Sprintf(".default(%s)", val))
			}
		case "ref":
			if ref := exprToString(c.Value); ref != "" {
				b.WriteString(fmt.Sprintf(".references(() => %s.id)", toCamelCase(ref)))
			}
		}
	}
}

// --- Validation ---

func (g *Generator) genValidation(endpoints []*ast.Endpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { z } from 'zod';\n\n")

	emitted := make(map[string]bool) // track emitted schema names

	for _, ep := range endpoints {
		// Collect non-path-param inputs
		var inputs []*ast.InputStmt
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					inputs = append(inputs, inp)
				}
			}
		}
		if len(inputs) == 0 {
			continue
		}

		schemaName := endpointSchemaName(ep.Method, ep.Path)
		if emitted[schemaName] {
			continue // skip duplicates
		}
		emitted[schemaName] = true

		isQuerySource := ep.Method == "GET" || ep.Method == "DELETE"

		b.WriteString(fmt.Sprintf("export const %s = z.object({\n", schemaName))
		for _, inp := range inputs {
			var zodType string
			if isQuerySource {
				zodType = typeToZodCoerce(inp.Type)
			} else {
				zodType = typeToZod(inp.Type)
			}
			zodType += constraintsToZod(inp.Constraints)
			b.WriteString(fmt.Sprintf("  %s: %s,\n", toCamelCase(inp.Name), zodType))
		}
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{Path: "src/validation/schemas.ts", Content: []byte(b.String())}
}

func endpointSchemaName(method, path string) string {
	// GET /api/todos -> getTodosSchema
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var meaningful []string
	for _, p := range parts {
		if p == "api" || p == "" || strings.HasPrefix(p, ":") {
			continue
		}
		meaningful = append(meaningful, toPascalCase(strings.ReplaceAll(p, "-", "_")))
	}
	return strings.ToLower(method) + strings.Join(meaningful, "") + "Schema"
}

// --- Index (entrypoint) ---

func (g *Generator) genIndex(bp *ast.Blueprint, endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint, middlewares []*ast.Middleware, subscribes []*ast.Subscribe) codegen.OutputFile {
	port := blueprintEntryInt(bp, "port")
	if port == 0 {
		port = 3000
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\nimport { serve } from '@hono/node-server';\n")

	// If there are WS endpoints, import createNodeWebSocket
	if len(ws) > 0 {
		b.WriteString("import { createNodeWebSocket } from '@hono/node-ws';\n")
	}

	b.WriteString("import { env } from './lib/env.js';\n")

	// Collect global middleware imports
	builtinMiddleware := map[string]string{
		"cors":     "hono/cors",
		"compress": "hono/compress",
	}
	for _, u := range bp.Uses {
		if pkg, ok := builtinMiddleware[u.Name]; ok {
			b.WriteString(fmt.Sprintf("import { %s } from '%s';\n", u.Name, pkg))
		} else {
			b.WriteString(fmt.Sprintf("import { %s } from './middleware/%s.js';\n",
				toCamelCase(u.Name), toKebabCase(u.Name)))
		}
	}

	// Build resource sets for conflict detection (same logic as generateAll)
	routeResources := make(map[string]bool)
	for _, ep := range endpoints {
		routeResources[extractResource(ep.Path)] = true
	}
	streamResources := make(map[string]bool)
	for _, se := range streams {
		streamResources[extractResource(se.Path)] = true
	}
	wsResources := make(map[string]bool)
	for _, we := range ws {
		wsResources[extractResource(we.Path)] = true
	}

	// fileKeyForStream/Ws mirrors the suffix logic in generateAll
	fileKeyForStream := func(res string) string {
		if routeResources[res] {
			return res + "-stream"
		}
		return res
	}
	fileKeyForWs := func(res string) string {
		if routeResources[res] || streamResources[res] {
			return res + "-ws"
		}
		return res
	}

	// Import regular route files
	for res := range routeResources {
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(res), toKebabCase(res)))
	}

	// Import stream route files
	for res := range streamResources {
		fk := fileKeyForStream(res)
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(fk), toKebabCase(fk)))
	}

	// Import WS route files
	for res := range wsResources {
		fk := fileKeyForWs(res)
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(fk), toKebabCase(fk)))
	}

	// Import subscription handlers and event emitter
	if len(subscribes) > 0 {
		b.WriteString("import { on } from './lib/events.js';\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			fileName := toKebabCase(strings.ReplaceAll(sub.Event, ".", "-"))
			b.WriteString(fmt.Sprintf("import { %s } from './subscriptions/%s.js';\n", handlerName, fileName))
		}
	}

	b.WriteString("\n")

	b.WriteString("const app = new Hono();\n\n")

	// If there are WS endpoints, set up upgradeWebSocket + injectWebSocket
	if len(ws) > 0 {
		b.WriteString("const { injectWebSocket, upgradeWebSocket } = createNodeWebSocket({ app });\n\n")
	}

	// Wire global middleware
	for _, u := range bp.Uses {
		if u.Body != nil && len(u.Body.Entries) > 0 {
			// Middleware with config (e.g., cors { origins: [...] })
			configParts := make([]string, len(u.Body.Entries))
			for i, kv := range u.Body.Entries {
				key := toCamelCase(kv.Key)
				// Map Blueprint config keys to framework-specific keys
				if u.Name == "cors" && key == "origins" {
					key = "origin"
				}
				configParts[i] = fmt.Sprintf("%s: %s", key, exprToJS(kv.Value))
			}
			b.WriteString(fmt.Sprintf("app.use('*', %s({ %s }));\n",
				toCamelCase(u.Name), strings.Join(configParts, ", ")))
		} else {
			// Simple middleware
			if _, ok := builtinMiddleware[u.Name]; ok {
				b.WriteString(fmt.Sprintf("app.use('*', %s());\n", toCamelCase(u.Name)))
			} else {
				b.WriteString(fmt.Sprintf("app.use('*', %s);\n", toCamelCase(u.Name)))
			}
		}
	}
	if len(bp.Uses) > 0 {
		b.WriteString("\n")
	}

	// Mount regular routes
	for res := range routeResources {
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(res)))
	}

	// Mount stream routes
	for res := range streamResources {
		fk := fileKeyForStream(res)
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(fk)))
	}

	// Mount WS routes
	for res := range wsResources {
		fk := fileKeyForWs(res)
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(fk)))
	}

	b.WriteString("\n")

	// Register event subscriptions
	if len(subscribes) > 0 {
		b.WriteString("// Register event subscriptions\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			b.WriteString(fmt.Sprintf("on('%s', %s);\n", sub.Event, handlerName))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(`console.log('%%s listening on port %%d', '%s', %d);`+"\n", bp.Name, port))

	// If WS: use injectWebSocket(serve(...)), otherwise plain serve(...)
	if len(ws) > 0 {
		b.WriteString(fmt.Sprintf("const server = serve({ fetch: app.fetch, port: %d });\n", port))
		b.WriteString("injectWebSocket(server);\n\n")
	} else {
		b.WriteString(fmt.Sprintf("serve({ fetch: app.fetch, port: %d });\n\n", port))
	}

	b.WriteString("export default app;\n")

	return codegen.OutputFile{Path: "src/index.ts", Content: []byte(b.String())}
}

// --- Routes ---

func (g *Generator) genRoute(resource string, endpoints []*ast.Endpoint, hasDB bool) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")

	// Determine what imports are needed
	schemaNames := make(map[string]bool)
	middlewareNames := make(map[string]bool)
	needsZValidator := false
	needsWebhookAuth := false

	// Check if any endpoint in this route file uses data operations (local check).
	// This supplements the global hasDB flag so that routes with data ops always
	// get db/schema imports, even if the check is done per-file rather than globally.
	needsDB := hasDB
	if !needsDB {
		for _, ep := range endpoints {
			if stmtsHaveDataOps(ep.Stmts) {
				needsDB = true
				break
			}
		}
	}

	for _, ep := range endpoints {
		// Check for non-path-param inputs
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					needsZValidator = true
					schemaNames[endpointSchemaName(ep.Method, ep.Path)] = true
				}
			}
		}
		// Check for middleware and auth
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				middlewareNames[meta.Use.Name] = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				needsWebhookAuth = true
			}
		}
	}

	if needsZValidator {
		b.WriteString("import { zValidator } from '@hono/zod-validator';\n")
	}
	if needsDB {
		b.WriteString("import { db } from '../lib/db.js';\n")
		b.WriteString("import * as schema from '../models/schema.js';\n")
		b.WriteString("import { eq, and, or, lt, gt, lte, gte, ne, sql, desc, asc, inArray } from 'drizzle-orm';\n")
	}
	// Import validation schemas
	if len(schemaNames) > 0 {
		names := sortedKeys2(schemaNames)
		b.WriteString(fmt.Sprintf("import { %s } from '../validation/schemas.js';\n",
			strings.Join(names, ", ")))
	}
	// Import middleware
	for _, mwName := range sortedKeys2(middlewareNames) {
		b.WriteString(fmt.Sprintf("import { %s } from '../middleware/%s.js';\n",
			toCamelCase(mwName), toKebabCase(mwName)))
	}
	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	if needsWebhookAuth {
		b.WriteString("import { createHmac, timingSafeEqual } from 'node:crypto';\n")
	}

	// Collect additional imports from endpoint bodies (pipes, functions, storage, env, enums)
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.Stmts))
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(resource) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	for _, ep := range endpoints {
		method := strings.ToLower(ep.Method)
		b.WriteString(fmt.Sprintf("// %s %s\n", ep.Method, ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		// Build handler: routeVar.method('path', ...middleware, zValidator, async (c) => { ... })
		b.WriteString(fmt.Sprintf("%s.%s('%s'", routeVar, method, ep.Path))

		// Add middleware
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				b.WriteString(fmt.Sprintf(", %s", toCamelCase(meta.Use.Name)))
			}
		}

		// Add zValidator for non-path-param inputs
		hasNonPathInputs := false
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					hasNonPathInputs = true
					break
				}
			}
		}
		if hasNonPathInputs {
			schemaName := endpointSchemaName(ep.Method, ep.Path)
			source := "'json'"
			if ep.Method == "GET" || ep.Method == "DELETE" {
				source = "'query'"
			}
			b.WriteString(fmt.Sprintf(", zValidator(%s, %s)", source, schemaName))
		}

		b.WriteString(", async (c) => {\n")

		// Emit meta as comments for rate limit, cache; webhook auth as verification code
		for _, meta := range ep.Meta {
			switch meta.Kind {
			case "limit":
				b.WriteString(fmt.Sprintf("  // rate limit: %s\n", exprToJS(meta.Value)))
			case "cache":
				b.WriteString(fmt.Sprintf("  // cache: %s\n", exprToJS(meta.Value)))
			case "auth":
				if secretKey := webhookAuthSecretKey(meta.Value); secretKey != "" {
					b.WriteString(fmt.Sprintf("  const _payload = await c.req.text();\n"))
					b.WriteString(fmt.Sprintf("  const _sig = c.req.header('X-Webhook-Signature') ?? '';\n"))
					b.WriteString(fmt.Sprintf("  const _expected = createHmac('sha256', process.env.%s!).update(_payload).digest('hex');\n", secretKey))
					b.WriteString("  if (_sig.length !== _expected.length || !timingSafeEqual(Buffer.from(_sig, 'hex'), Buffer.from(_expected, 'hex'))) return c.json({ error: 'Invalid signature' }, 401);\n")
					b.WriteString("  let data: any;\n")
					b.WriteString("  try { data = JSON.parse(_payload); } catch { return c.json({ error: 'Invalid payload' }, 400); }\n")
				}
			}
		}

		b.WriteString("  try {\n")

		// Build context vars from middleware inject statements
		ctxVars := make(map[string]bool)
		boundVars := make(map[string]string)
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				if mw, ok := g.middlewares[meta.Use.Name]; ok {
					for k, v := range extractInjectedVars(mw.Before) {
						ctxVars[k] = v
					}
					for k, v := range extractInjectedVars(mw.After) {
						ctxVars[k] = v
					}
					// Map model names to their context variable names
					// e.g., api_key -> auth (from "inject key as auth" where key was queried from api_key)
					for modelName, ctxName := range extractInjectedModelMap(mw.Before) {
						boundVars[modelName] = "(c as any).get('" + toCamelCase(ctxName) + "')"
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.After) {
						boundVars[modelName] = "(c as any).get('" + toCamelCase(ctxName) + "')"
					}
				}
			}
		}

		// Emit arrow statements with endpoint context
		ctx := emitCtx{
			kind:        "endpoint",
			method:      ep.Method,
			path:        ep.Path,
			ctxVars:     ctxVars,
			boundVars:   boundVars,
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)

		b.WriteString("  } catch (err) {\n")
		b.WriteString("    if (err instanceof BpError) return c.json({ error: err.message }, err.statusCode as any);\n")
		b.WriteString("    throw err;\n")
		b.WriteString("  }\n")
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(resource)),
		Content: []byte(b.String()),
	}
}

// sortedKeys2 returns sorted keys from a map[string]bool.
func sortedKeys2(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectBindings returns the set of variable names bound in a list of arrow statements.
func collectBindings(stmts []ast.ArrowStmt) map[string]bool {
	bindings := make(map[string]bool)
	for _, s := range stmts {
		if step, ok := s.(*ast.StepStmt); ok && step.Binding != "" {
			bindings[toCamelCase(step.Binding)] = true
		}
	}
	return bindings
}

// collectReferencedIdents returns all identifier names referenced in a list of arrow statements.
func collectReferencedIdents(stmts []ast.ArrowStmt) map[string]bool {
	refs := make(map[string]bool)
	for _, s := range stmts {
		collectIdentsFromStmt(s, refs)
	}
	return refs
}

func collectIdentsFromStmt(s ast.ArrowStmt, refs map[string]bool) {
	switch v := s.(type) {
	case *ast.StepStmt:
		collectIdentsFromExpr(v.Expr, refs)
	case *ast.OutputStmt:
		if v.Value != nil {
			collectIdentsFromExpr(v.Value, refs)
		}
	case *ast.GuardStmt:
		collectIdentsFromExpr(v.Condition, refs)
	case *ast.WhenStmt:
		collectIdentsFromExpr(v.Condition, refs)
		if v.Inline != nil {
			collectIdentsFromExpr(v.Inline, refs)
		}
		for _, stmt := range v.Body {
			collectIdentsFromStmt(stmt, refs)
		}
	case *ast.TryRecover:
		for _, stmt := range v.Try {
			collectIdentsFromStmt(stmt, refs)
		}
		for _, stmt := range v.Recover {
			collectIdentsFromStmt(stmt, refs)
		}
	}
}

func collectIdentsFromExpr(e ast.Expr, refs map[string]bool) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.Ident:
		refs[toCamelCase(v.Name)] = true
	case *ast.FieldAccess:
		collectIdentsFromExpr(v.Base, refs)
	case *ast.IndexAccess:
		collectIdentsFromExpr(v.Base, refs)
		collectIdentsFromExpr(v.Index, refs)
	case *ast.BinaryExpr:
		collectIdentsFromExpr(v.Left, refs)
		collectIdentsFromExpr(v.Right, refs)
	case *ast.UnaryExpr:
		collectIdentsFromExpr(v.Operand, refs)
	case *ast.ParenExpr:
		collectIdentsFromExpr(v.Expr, refs)
	case *ast.FnCall:
		for _, a := range v.Args {
			collectIdentsFromExpr(a, refs)
		}
	case *ast.ListExpr:
		for _, el := range v.Elements {
			collectIdentsFromExpr(el, refs)
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			collectIdentsFromExpr(kv.Value, refs)
		}
	}
}

// collectStepBindingsReassigned checks which input variable names are later reassigned in steps.
func collectStepBindingsReassigned(stmts []ast.ArrowStmt) map[string]bool {
	inputNames := make(map[string]bool)
	reassigned := make(map[string]bool)

	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.InputStmt:
			inputNames[toCamelCase(v.Name)] = true
		case *ast.StepStmt:
			if v.Binding != "" {
				name := toCamelCase(v.Binding)
				if inputNames[name] {
					reassigned[name] = true
				}
			}
		}
	}
	return reassigned
}

// collectPropertyMutations returns the set of variable names that have property mutations
// via `when cond: var.field = value` inline assignment patterns.
// These need `Record<string, any>` type annotation.
func collectPropertyMutations(stmts []ast.ArrowStmt) map[string]bool {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		if when, ok := stmt.(*ast.WhenStmt); ok && when.Inline != nil {
			if bin, ok := when.Inline.(*ast.BinaryExpr); ok && bin.Op == "=" {
				if fa, ok := bin.Left.(*ast.FieldAccess); ok {
					if ident, ok := fa.Base.(*ast.Ident); ok {
						mutated[toCamelCase(ident.Name)] = true
					}
				}
			}
		}
	}
	return mutated
}

// emitArrowStmts generates JavaScript for a sequence of arrow statements.
func (g *Generator) emitArrowStmts(b *strings.Builder, stmts []ast.ArrowStmt, indent string, ctx emitCtx) {
	// Initialize context maps if nil
	if ctx.boundVars == nil {
		ctx.boundVars = make(map[string]string)
	}
	if ctx.declared == nil {
		ctx.declared = make(map[string]bool)
	}
	if ctx.varModels == nil {
		ctx.varModels = make(map[string]string)
	}
	if ctx.singleVars == nil {
		ctx.singleVars = make(map[string]bool)
	}

	// Pre-scan: find input variables that are later reassigned (for let vs const)
	reassigned := collectStepBindingsReassigned(stmts)

	// Pre-scan: find variables that have property mutations (e.g., `when x: filters.status = x`)
	// These need `Record<string, any>` type annotation
	propMutated := collectPropertyMutations(stmts)

	// Pre-scan: for try blocks, find which bindings need hoisting
	// (bindings in try that are referenced by stmts after the try)
	hoisted := make(map[string]bool)
	for i, stmt := range stmts {
		if tr, ok := stmt.(*ast.TryRecover); ok {
			tryBindings := collectBindings(tr.Try)
			// Collect references in all stmts after this try/recover
			afterStmts := stmts[i+1:]
			afterRefs := collectReferencedIdents(afterStmts)
			for name := range tryBindings {
				if afterRefs[name] {
					hoisted[name] = true
				}
			}
		}
	}

	// Emit hoisted variable declarations before the main body
	for name := range hoisted {
		b.WriteString(fmt.Sprintf("%slet %s: any;\n", indent, name))
		ctx.declared[name] = true
	}

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.InputStmt:
			name := toCamelCase(s.Name)
			if ctx.kind == "endpoint" {
				decl := "const"
				if reassigned[name] {
					decl = "let"
				}
				if isPathParam(s.Name, ctx.path) {
					b.WriteString(fmt.Sprintf("%s%s %s = c.req.param('%s');\n",
						indent, decl, name, s.Name))
				} else if ctx.method == "GET" || ctx.method == "DELETE" {
					b.WriteString(fmt.Sprintf("%s%s %s = c.req.valid('query').%s;\n",
						indent, decl, name, name))
				} else {
					b.WriteString(fmt.Sprintf("%s%s %s = c.req.valid('json').%s;\n",
						indent, decl, name, name))
				}
				ctx.declared[name] = true
			}
			// In function/pipe context, inputs are function params — skip

		case *ast.StepStmt:
			// Track data-op bindings BEFORE codegen so context is available
			if s.Binding != "" {
				if fn, ok := s.Expr.(*ast.FnCall); ok && isDataOp(fn.Name) {
					if len(fn.Args) > 0 {
						if ident, ok := fn.Args[0].(*ast.Ident); ok {
							name := toCamelCase(s.Binding)
							ctx.boundVars[ident.Name] = name
							ctx.varModels[s.Binding] = ident.Name
							// fetch returns a single record; query returns a collection
							if fn.Name == "fetch" {
								ctx.singleVars[s.Binding] = true
							}
						}
					}
				}
			}

			// Special-case: delete <variable> where variable holds tracked model records
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "delete" && len(fn.Args) > 0 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						varName := toCamelCase(ident.Name)
						schemaTable := "schema." + toCamelCase(modelName)
						if ctx.singleVars[ident.Name] {
							// Single record from fetch — use eq
							b.WriteString(fmt.Sprintf("%sawait db.delete(%s).where(eq(%s.id, %s.id));\n",
								indent, schemaTable, schemaTable, varName))
						} else {
							// Collection from query — bulk delete
							b.WriteString(fmt.Sprintf("%sawait db.delete(%s).where(inArray(%s.id, %s.map((r: any) => r.id)));\n",
								indent, schemaTable, schemaTable, varName))
						}
						continue
					}
				}
			}

			// Special-case: map <variable>: <expr> — use model name as loop param and fix inner boundVars
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "map" && len(fn.Args) >= 2 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						collectionVar := toCamelCase(ident.Name)
						itemVar := toCamelCase(modelName)
						// Create inner ctx where boundVars maps modelName → itemVar
						// so update/delete in the body use itemVar.id (the loop param) not collection.id
						innerCtx := ctx
						innerCtx.boundVars = make(map[string]string)
						for k, v := range ctx.boundVars {
							innerCtx.boundVars[k] = v
						}
						innerCtx.boundVars[modelName] = itemVar
						body := exprToJSWithCtx(fn.Args[1], &innerCtx)
						b.WriteString(fmt.Sprintf("%sawait Promise.all(%s.map(async (%s: any) => %s));\n",
							indent, collectionVar, itemVar, body))
						continue
					}
				}
			}

			expr := exprToJSWithCtx(s.Expr, &ctx)
			if s.Binding != "" {
				name := toCamelCase(s.Binding)

				if ctx.declared[name] || hoisted[name] {
					// Already declared (input reassignment or hoisted) — plain assignment
					b.WriteString(fmt.Sprintf("%s%s = %s;\n", indent, name, expr))
				} else {
					// Use `let` for reassigned vars; annotate filter-accumulator objects as Record<string, any>
					decl := "const"
					if reassigned[name] {
						decl = "let"
					}
					if propMutated[name] {
						b.WriteString(fmt.Sprintf("%s%s %s: Record<string, any> = %s;\n", indent, decl, name, expr))
					} else {
						b.WriteString(fmt.Sprintf("%s%s %s = %s;\n", indent, decl, name, expr))
					}
					ctx.declared[name] = true
				}
			} else {
				b.WriteString(fmt.Sprintf("%s%s;\n", indent, expr))
			}

		case *ast.GuardStmt:
			cond := exprToJSWithCtx(s.Condition, &ctx)
			if ctx.kind == "endpoint" {
				b.WriteString(fmt.Sprintf("%sif (!(%s)) return c.json({ error: %q }, %s as any);\n",
					indent, cond, s.Message, s.Status))
			} else {
				b.WriteString(fmt.Sprintf("%sif (!(%s)) throw new BpError(%s, %q);\n",
					indent, cond, s.Status, s.Message))
			}

		case *ast.WhenStmt:
			cond := exprToJSWithCtx(s.Condition, &ctx)
			if s.Inline != nil {
				b.WriteString(fmt.Sprintf("%sif (%s) %s;\n", indent, cond, exprToJSWithCtx(s.Inline, &ctx)))
			} else if len(s.Body) > 0 {
				b.WriteString(fmt.Sprintf("%sif (%s) {\n", indent, cond))
				// Use a block-scoped copy of ctx so declarations inside this when-block
				// don't leak into sibling when-blocks (they're in separate JS if-scopes).
				innerCtx := ctx
				innerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					innerCtx.declared[k] = v
				}
				g.emitArrowStmts(b, s.Body, indent+"  ", innerCtx)
				b.WriteString(fmt.Sprintf("%s}\n", indent))
			}

		case *ast.OutputStmt:
			status := s.Status
			if status == "" {
				status = "200"
			}
			val := exprToJSWithCtx(s.Value, &ctx)

			if ctx.kind == "endpoint" {
				// For 204 No Content, use c.body(null, 204)
				if status == "204" {
					b.WriteString(fmt.Sprintf("%sreturn c.body(null, 204);\n", indent))
				} else {
					b.WriteString(fmt.Sprintf("%sreturn c.json(%s, %s as any);\n", indent, val, status))
				}
			} else {
				// function/pipe/middleware context — plain return
				b.WriteString(fmt.Sprintf("%sreturn %s;\n", indent, val))
			}

		case *ast.TryRecover:
			b.WriteString(fmt.Sprintf("%stry {\n", indent))
			g.emitArrowStmts(b, s.Try, indent+"  ", ctx)
			b.WriteString(fmt.Sprintf("%s} catch (error: any) {\n", indent))
			g.emitArrowStmts(b, s.Recover, indent+"  ", ctx)
			b.WriteString(fmt.Sprintf("%s}\n", indent))
		}
	}
}

// writeDataImports adds standard data-access imports (db, schema, drizzle-orm operators)
// to a file builder. Call this for functions, schedules, workers, pipes, and middleware
// that contain data operations.
func writeDataImports(b *strings.Builder) {
	b.WriteString("import { db } from '../lib/db.js';\n")
	b.WriteString("import * as schema from '../models/schema.js';\n")
	b.WriteString("import { eq, ne, lt, gt, lte, gte, and, or, sql, desc, asc, inArray } from 'drizzle-orm';\n")
}

// stmtsHaveDataOps walks a list of arrow statements and returns true if any
// contain data operation calls (query, save, fetch, update, delete, etc.).
func stmtsHaveDataOps(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		if hasDataOpsInStmt(s) {
			return true
		}
	}
	return false
}

func hasDataOpsInStmt(s ast.ArrowStmt) bool {
	switch v := s.(type) {
	case *ast.StepStmt:
		return exprHasDataOp(v.Expr)
	case *ast.OutputStmt:
		if v.Value != nil {
			return exprHasDataOp(v.Value)
		}
	case *ast.GuardStmt:
		return exprHasDataOp(v.Condition)
	case *ast.WhenStmt:
		if v.Inline != nil && exprHasDataOp(v.Inline) {
			return true
		}
		if stmtsHaveDataOps(v.Body) {
			return true
		}
	case *ast.TryRecover:
		if stmtsHaveDataOps(v.Try) {
			return true
		}
		if stmtsHaveDataOps(v.Recover) {
			return true
		}
	}
	return false
}

func exprHasDataOp(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.FnCall:
		if isDataOp(v.Name) {
			return true
		}
		// Recurse into function arguments to find nested data ops
		// e.g. map(items, save(...)) or pipe(query(...))
		for _, arg := range v.Args {
			if exprHasDataOp(arg) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return exprHasDataOp(v.Left) || exprHasDataOp(v.Right)
	case *ast.UnaryExpr:
		return exprHasDataOp(v.Operand)
	case *ast.ParenExpr:
		return exprHasDataOp(v.Expr)
	case *ast.BlockExpr:
		// Recurse into block expression entries for nested data ops
		for _, kv := range v.Entries {
			if exprHasDataOp(kv.Value) {
				return true
			}
		}
	}
	return false
}

// --- Import Collection ---

// importCollector tracks what imports are needed for a generated file.
type importCollector struct {
	needsEnv        bool
	fnCalls         map[string]bool // user-declared fn names called
	pipeCalls       map[string]bool // user-declared pipe names called
	storageOps      map[string]bool // storage operations (upload, download)
	modelTypes      map[string]bool // model type names for type imports
	enumTypes       map[string]bool // enum type names for value imports
	structEnumTypes map[string]bool // struct enum names that also need <Name>Config import
	unknownCalls    map[string]bool // unrecognized calls (emit stubs)
	externalCalls   map[string]bool // external service call function names (call<Service>)
}

func newImportCollector() *importCollector {
	return &importCollector{
		fnCalls:         make(map[string]bool),
		pipeCalls:       make(map[string]bool),
		storageOps:      make(map[string]bool),
		modelTypes:      make(map[string]bool),
		enumTypes:       make(map[string]bool),
		structEnumTypes: make(map[string]bool),
		unknownCalls:    make(map[string]bool),
		externalCalls:   make(map[string]bool),
	}
}

// merge combines another collector's references into this one.
func (ic *importCollector) merge(other *importCollector) {
	if other.needsEnv {
		ic.needsEnv = true
	}
	for k := range other.fnCalls {
		ic.fnCalls[k] = true
	}
	for k := range other.pipeCalls {
		ic.pipeCalls[k] = true
	}
	for k := range other.storageOps {
		ic.storageOps[k] = true
	}
	for k := range other.modelTypes {
		ic.modelTypes[k] = true
	}
	for k := range other.enumTypes {
		ic.enumTypes[k] = true
	}
	for k := range other.structEnumTypes {
		ic.structEnumTypes[k] = true
	}
	for k := range other.unknownCalls {
		ic.unknownCalls[k] = true
	}
	for k := range other.externalCalls {
		ic.externalCalls[k] = true
	}
}

// collectImports scans arrow statements for import needs.
func (g *Generator) collectImports(stmts []ast.ArrowStmt) *importCollector {
	ic := newImportCollector()
	walkStmts(stmts, func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.FieldAccess:
			// env.X references
			if ident, ok := v.Base.(*ast.Ident); ok {
				if ident.Name == "env" {
					ic.needsEnv = true
				}
				// Enum.variant references (e.g., Plan.free)
				if g.declaredEnums[ident.Name] {
					ic.enumTypes[ident.Name] = true
				}
			}
		case *ast.FnCall:
			if isDataOp(v.Name) || isBuiltinFn(v.Name) {
				return
			}
			if v.Name == "upload" || v.Name == "download" {
				ic.storageOps[v.Name] = true
				return
			}
			// call <service> METHOD /path — track the service for external import
			if v.Name == "call" && len(v.Args) >= 1 {
				if svc, ok := v.Args[0].(*ast.Ident); ok {
					normalized := normalizeServiceName(svc.Name)
					if g.declaredExternals[normalized] {
						ic.externalCalls[normalized] = true
					}
				}
				return
			}
			if g.declaredPipes[v.Name] {
				ic.pipeCalls[v.Name] = true
			} else if g.declaredFns[v.Name] {
				ic.fnCalls[v.Name] = true
			} else {
				ic.unknownCalls[v.Name] = true
			}
		case *ast.IndexAccess:
			// Enum[key] references (e.g., Plan[key.plan])
			if ident, ok := v.Base.(*ast.Ident); ok {
				if g.declaredEnums[ident.Name] {
					ic.enumTypes[ident.Name] = true
					if g.structEnums[ident.Name] {
						ic.structEnumTypes[ident.Name] = true
					}
				}
			}
		}
	})
	return ic
}

// writeImports writes import statements based on collected references.
func (ic *importCollector) writeImports(b *strings.Builder, hasStorage bool) {
	if ic.needsEnv {
		b.WriteString("import { env } from '../lib/env.js';\n")
	}
	for _, name := range sortedKeys2(ic.fnCalls) {
		b.WriteString(fmt.Sprintf("import { %s } from '../functions/%s.js';\n",
			toCamelCase(name), toKebabCase(name)))
	}
	for _, name := range sortedKeys2(ic.pipeCalls) {
		b.WriteString(fmt.Sprintf("import { %s } from '../pipes/%s.js';\n",
			toCamelCase(name), toKebabCase(name)))
	}
	if len(ic.storageOps) > 0 {
		if hasStorage {
			ops := sortedKeys2(ic.storageOps)
			b.WriteString(fmt.Sprintf("import { %s } from '../lib/storage.js';\n",
				strings.Join(ops, ", ")))
		} else {
			for k := range ic.storageOps {
				ic.unknownCalls[k] = true
			}
		}
	}
	if len(ic.modelTypes) > 0 {
		names := sortedKeys2(ic.modelTypes)
		tsNames := make([]string, len(names))
		for i, n := range names {
			tsNames[i] = toPascalCase(n)
		}
		b.WriteString(fmt.Sprintf("import type { %s } from '../models/schema.js';\n",
			strings.Join(tsNames, ", ")))
	}
	if len(ic.enumTypes) > 0 {
		names := sortedKeys2(ic.enumTypes)
		// Include <Name>Config for struct enums that use bracket access
		var exports []string
		for _, n := range names {
			exports = append(exports, n)
			if ic.structEnumTypes[n] {
				exports = append(exports, n+"Config")
			}
		}
		b.WriteString(fmt.Sprintf("import { %s } from '../types.js';\n",
			strings.Join(exports, ", ")))
	}
	// Emit imports for external service call functions
	if len(ic.externalCalls) > 0 {
		names := sortedKeys2(ic.externalCalls)
		callFns := make([]string, len(names))
		for i, n := range names {
			callFns[i] = "call" + toPascalCase(n)
		}
		b.WriteString(fmt.Sprintf("import { %s } from '../lib/external.js';\n",
			strings.Join(callFns, ", ")))
	}
	// Emit stubs for unrecognized function calls
	for _, name := range sortedKeys2(ic.unknownCalls) {
		jsName := toCamelCase(name)
		b.WriteString(fmt.Sprintf("\n// TODO: implement %s\nasync function %s(...args: any[]): Promise<any> { return undefined; }\n",
			name, jsName))
	}
}

// --- Functions ---

func (g *Generator) genFunction(fn *ast.Fn) []codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Collect imports from function body
	var ic *importCollector
	if fn.Logic != nil {
		ic = g.collectImports(fn.Logic.Stmts)
	} else {
		ic = newImportCollector()
	}

	// Check param types for model/enum type references.
	// Model-typed params are emitted as `any` to avoid Drizzle nullable field issues,
	// so we don't add them to modelTypes imports.
	for _, inp := range fn.Inputs {
		if nt, ok := inp.Type.(*ast.NamedType); ok {
			if !g.declaredModels[nt.Name] {
				if g.declaredEnums[nt.Name] {
					ic.enumTypes[nt.Name] = true
				}
			}
		}
	}

	// Add data imports if function logic uses data operations
	if fn.Logic != nil && stmtsHaveDataOps(fn.Logic.Stmts) {
		writeDataImports(&b)
	}

	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(fn.Name)

	// Input params — model-typed params use `any` to avoid Drizzle nullable field issues
	params := make([]string, len(fn.Inputs))
	for i, inp := range fn.Inputs {
		paramType := typeToTS(inp.Type)
		if nt, ok := inp.Type.(*ast.NamedType); ok && g.declaredModels[nt.Name] {
			paramType = "any"
		}
		params[i] = fmt.Sprintf("%s: %s", toCamelCase(inp.Name), paramType)
	}

	// Return type
	retType := "void"
	if len(fn.Outputs) > 0 && fn.Outputs[0].Value != nil {
		// Try to infer from output type if possible
		retType = "any"
	}

	var extraFiles []codegen.OutputFile
	if fn.Impl != nil {
		switch fn.Impl.Strategy {
		case "exec":
			// impl exec { cmd: "script.sh" [, args: [...]] }
			// Generates a function that runs a child process via execFile.
			b.WriteString("import { execFile } from 'child_process';\n")
			b.WriteString("import { promisify } from 'util';\n\n")
			b.WriteString("const execFileAsync = promisify(execFile);\n\n")
			cmd := ""
			var extraArgs []string
			for _, kv := range fn.Impl.Entries {
				switch kv.Key {
				case "cmd":
					cmd = exprToString(kv.Value)
				case "args":
					if list, ok := kv.Value.(*ast.ListExpr); ok {
						for _, el := range list.Elements {
							extraArgs = append(extraArgs, exprToJS(el))
						}
					}
				}
			}
			inputArgs := make([]string, len(fn.Inputs))
			for i, inp := range fn.Inputs {
				inputArgs[i] = toCamelCase(inp.Name)
			}
			allArgs := append(extraArgs, inputArgs...) //nolint:gocritic
			argsJS := ""
			if len(allArgs) > 0 {
				argsJS = strings.Join(allArgs, ", ")
			}
			b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType))
			b.WriteString(fmt.Sprintf("  const { stdout } = await execFileAsync(%q, [%s]);\n", cmd, argsJS))
			b.WriteString("  return stdout;\n")
			b.WriteString("}\n")

		case "http":
			// impl http { method: POST, url: "https://...", body: {...} }
			// Generates a function that performs a fetch call.
			method := "GET"
			url := ""
			bodyExpr := ""
			for _, kv := range fn.Impl.Entries {
				switch kv.Key {
				case "method":
					method = strings.ToUpper(exprToString(kv.Value))
				case "url":
					url = exprToString(kv.Value)
				case "body":
					bodyExpr = exprToJS(kv.Value)
				}
			}
			b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType))
			if bodyExpr != "" {
				b.WriteString(fmt.Sprintf("  const res = await fetch(%q, {\n", url))
				b.WriteString(fmt.Sprintf("    method: %q,\n", method))
				b.WriteString(fmt.Sprintf("    body: JSON.stringify(%s),\n", bodyExpr))
				b.WriteString("    headers: { 'Content-Type': 'application/json' },\n")
				b.WriteString("  });\n")
			} else {
				b.WriteString(fmt.Sprintf("  const res = await fetch(%q, { method: %q });\n", url, method))
			}
			b.WriteString("  return res.json();\n")
			b.WriteString("}\n")

		default:
			// "node" strategy or unrecognized — native module implementation
			for _, kv := range fn.Impl.Entries {
				if kv.Key == "module" {
					mod := exprToString(kv.Value)
					// Ensure .js extension for ESM compatibility
					if !strings.HasSuffix(mod, ".js") {
						mod = mod + ".js"
					}
					funcName := name
					for _, kv2 := range fn.Impl.Entries {
						if kv2.Key == "func" {
							funcName = exprToString(kv2.Value)
						}
					}
					b.WriteString(fmt.Sprintf("import { %s as %sImpl } from '%s';\n\n", funcName, name, mod))
					b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
						name, strings.Join(params, ", "), retType))
					args := make([]string, len(fn.Inputs))
					for i, inp := range fn.Inputs {
						args[i] = toCamelCase(inp.Name)
					}
					b.WriteString(fmt.Sprintf("  return %sImpl(%s);\n", name, strings.Join(args, ", ")))
					b.WriteString("}\n")
					// Generate a stub for local internal module paths (./internal/...)
					rawMod := strings.TrimSuffix(mod, ".js")
					if strings.HasPrefix(rawMod, "./internal/") {
						stubPath := fmt.Sprintf("src/functions/%s.ts", strings.TrimPrefix(rawMod, "./"))
						var sb strings.Builder
						sb.WriteString(fmt.Sprintf("// Stub generated by Blueprint — implement %s here\n", funcName))
						sb.WriteString(fmt.Sprintf("export async function %s(...args: any[]): Promise<any> {\n", funcName))
						sb.WriteString(fmt.Sprintf("  throw new Error('Not implemented: %s');\n", funcName))
						sb.WriteString("}\n")
						extraFiles = append(extraFiles, codegen.OutputFile{
							Path:    stubPath,
							Content: []byte(sb.String()),
						})
					}
					break
				}
			}
		}
	} else if fn.Logic != nil {
		// Inline logic — populate boundVars/varModels from function inputs
		fnCtx := emitCtx{
			kind:        "function",
			boundVars:   make(map[string]string),
			varModels:   make(map[string]string),
			singleVars:  make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		for _, inp := range fn.Inputs {
			if nt, ok := inp.Type.(*ast.NamedType); ok && g.declaredModels[nt.Name] {
				fnCtx.boundVars[nt.Name] = toCamelCase(inp.Name)
				fnCtx.varModels[toCamelCase(inp.Name)] = nt.Name
			}
		}
		b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
			name, strings.Join(params, ", "), retType))
		g.emitArrowStmts(&b, fn.Logic.Stmts, "  ", fnCtx)
		b.WriteString("}\n")
	}

	mainFile := codegen.OutputFile{
		Path:    fmt.Sprintf("src/functions/%s.ts", toKebabCase(fn.Name)),
		Content: []byte(b.String()),
	}
	return append([]codegen.OutputFile{mainFile}, extraFiles...)
}

// --- Pipes ---

func (g *Generator) genPipe(p *ast.Pipe) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { BpError } from '../lib/errors.js';\n")

	// Add data imports if pipe logic uses data operations
	if stmtsHaveDataOps(p.Stmts) {
		writeDataImports(&b)
	}

	// Add imports for env, functions, enums, etc.
	ic := g.collectImports(p.Stmts)
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(p.Name)

	// Extract input from first arrow stmt
	params := "input: any"
	for _, s := range p.Stmts {
		if inp, ok := s.(*ast.InputStmt); ok {
			params = fmt.Sprintf("%s: %s", toCamelCase(inp.Name), typeToTS(inp.Type))
			break
		}
	}

	b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<any> {\n", name, params))
	ctx := emitCtx{kind: "function"}
	for _, s := range p.Stmts {
		if _, ok := s.(*ast.InputStmt); ok {
			continue // skip inputs
		}
		g.emitArrowStmts(&b, []ast.ArrowStmt{s}, "  ", ctx)
	}
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/pipes/%s.ts", toKebabCase(p.Name)),
		Content: []byte(b.String()),
	}
}

// --- Middleware ---

func (g *Generator) genMiddleware(mw *ast.Middleware) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { createMiddleware } from 'hono/factory';\n")
	b.WriteString("import { BpError } from '../lib/errors.js';\n")

	// Add data imports if middleware logic uses data operations
	if stmtsHaveDataOps(mw.Before) || stmtsHaveDataOps(mw.After) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, enums, etc.
	ic := g.collectImports(mw.Before)
	ic.merge(g.collectImports(mw.After))
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(mw.Name)

	b.WriteString(fmt.Sprintf("export const %s = createMiddleware(async (c, next) => {\n", name))

	ctx := emitCtx{kind: "middleware"}

	if len(mw.Before) > 0 {
		b.WriteString("  // before\n")
		g.emitArrowStmts(&b, mw.Before, "  ", ctx)
	}

	b.WriteString("  await next();\n")

	if len(mw.After) > 0 {
		b.WriteString("  // after\n")
		g.emitArrowStmts(&b, mw.After, "  ", ctx)
	}

	b.WriteString("});\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/middleware/%s.ts", toKebabCase(mw.Name)),
		Content: []byte(b.String()),
	}
}

// --- Workers ---

func (g *Generator) genWorker(w *ast.Worker) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Add data imports if worker logic uses data operations
	if stmtsHaveDataOps(w.Stmts) || stmtsHaveDataOps(w.OnFail) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, storage, enums, etc.
	ic := g.collectImports(w.Stmts)
	ic.merge(g.collectImports(w.OnFail))
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(w.Name)
	b.WriteString(fmt.Sprintf("// Worker: %s\n\n", name))

	ctx := emitCtx{kind: "function"}
	b.WriteString(fmt.Sprintf("export async function %s(data: any): Promise<void> {\n", name))
	g.emitArrowStmts(&b, w.Stmts, "  ", ctx)
	if len(w.OnFail) > 0 {
		b.WriteString("}\n\n")
		b.WriteString(fmt.Sprintf("export async function %sOnFail(data: any, error: Error): Promise<void> {\n", name))
		g.emitArrowStmts(&b, w.OnFail, "  ", ctx)
	}
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/workers/%s.ts", toKebabCase(w.Name)),
		Content: []byte(b.String()),
	}
}

// --- Schedules ---

func (g *Generator) genSchedule(s *ast.Schedule) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Add data imports if schedule logic uses data operations
	if stmtsHaveDataOps(s.Stmts) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, storage, enums, etc.
	ic := g.collectImports(s.Stmts)
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(s.Name)
	b.WriteString(fmt.Sprintf("// Schedule: %s — cron: %s\n\n", name, s.Cron))

	ctx := emitCtx{kind: "function"}
	b.WriteString(fmt.Sprintf("export const %sCron = '%s';\n\n", name, s.Cron))
	b.WriteString(fmt.Sprintf("export async function %s(): Promise<void> {\n", name))
	g.emitArrowStmts(&b, s.Stmts, "  ", ctx)
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/schedules/%s.ts", toKebabCase(s.Name)),
		Content: []byte(b.String()),
	}
}

// --- Stream Endpoints ---

// genStreamRoute generates a route file for a group of stream (SSE) endpoints sharing the same resource.
// fileKey is the filename stem (may have "-stream" suffix when conflicting with a REST route).
func (g *Generator) genStreamRoute(resource, fileKey string, endpoints []*ast.StreamEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")
	b.WriteString("import { streamSSE } from 'hono/streaming';\n")

	// Collect imports from all endpoint bodies
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.Stmts))
		for _, h := range ep.Handlers {
			ic.merge(g.collectImports(h.Body))
		}
	}

	// Check if any endpoint uses data operations
	needsDB := false
	for _, ep := range endpoints {
		if stmtsHaveDataOps(ep.Stmts) {
			needsDB = true
			break
		}
		for _, h := range ep.Handlers {
			if stmtsHaveDataOps(h.Body) {
				needsDB = true
				break
			}
		}
	}
	if needsDB {
		writeDataImports(&b)
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	for _, ep := range endpoints {
		b.WriteString(fmt.Sprintf("// STREAM %s\n", ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		b.WriteString(fmt.Sprintf("%s.get('%s', async (c) => {\n", routeVar, ep.Path))
		b.WriteString("  return streamSSE(c, async (stream) => {\n")

		ctx := emitCtx{
			kind:        "function",
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}

		// Emit setup statements
		if len(ep.Stmts) > 0 {
			g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)
		}

		// Emit each handler block
		for _, h := range ep.Handlers {
			if h.EventName != "" {
				b.WriteString(fmt.Sprintf("    // on event: %s\n", h.EventName))
			}
			if h.Condition != nil {
				cond := exprToJSWithCtx(h.Condition, &ctx)
				b.WriteString(fmt.Sprintf("    if (%s) {\n", cond))

				handlerCtx := ctx
				handlerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					handlerCtx.declared[k] = v
				}
				// Emit the handler body statements
				g.emitArrowStmts(&b, h.Body, "      ", handlerCtx)
				// Emit a stream.writeSSE call for the event
				b.WriteString(fmt.Sprintf("      await stream.writeSSE({ event: '%s', data: JSON.stringify({}) });\n", h.EventName))
				b.WriteString("    }\n")
			} else {
				// Emit the handler body statements
				g.emitArrowStmts(&b, h.Body, "    ", ctx)
				// Emit stream.writeSSE call
				b.WriteString(fmt.Sprintf("    await stream.writeSSE({ event: '%s', data: JSON.stringify({}) });\n", h.EventName))
			}
		}

		b.WriteString("  });\n")
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(fileKey)),
		Content: []byte(b.String()),
	}
}

// --- WebSocket Endpoints ---

// genWsRoute generates a route file for a group of WebSocket endpoints sharing the same resource.
// fileKey is the filename stem (may have "-ws" suffix when conflicting with REST/stream route files).
func (g *Generator) genWsRoute(resource, fileKey string, endpoints []*ast.WsEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")
	b.WriteString("import { upgradeWebSocket } from 'hono/ws';\n")

	// Collect imports from all endpoint bodies
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.OnConnect))
		ic.merge(g.collectImports(ep.OnMessage))
		ic.merge(g.collectImports(ep.OnDisconnect))
	}

	// Check if any endpoint uses data operations
	needsDB := false
	for _, ep := range endpoints {
		if stmtsHaveDataOps(ep.OnConnect) || stmtsHaveDataOps(ep.OnMessage) || stmtsHaveDataOps(ep.OnDisconnect) {
			needsDB = true
			break
		}
	}
	if needsDB {
		writeDataImports(&b)
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	for _, ep := range endpoints {
		b.WriteString(fmt.Sprintf("// WS %s\n", ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		b.WriteString(fmt.Sprintf("%s.get('%s', upgradeWebSocket((c) => ({\n", routeVar, ep.Path))

		ctx := emitCtx{
			kind:        "function",
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}

		// onOpen handler
		b.WriteString("  onOpen(event, ws) {\n")
		if len(ep.OnConnect) > 0 {
			onConnectCtx := ctx
			onConnectCtx.declared = make(map[string]bool)
			g.emitArrowStmts(&b, ep.OnConnect, "    ", onConnectCtx)
		}
		b.WriteString("  },\n")

		// onMessage handler
		b.WriteString("  onMessage(event, ws) {\n")
		b.WriteString("    const message = event.data;\n")
		if len(ep.OnMessage) > 0 {
			onMessageCtx := ctx
			onMessageCtx.declared = make(map[string]bool)
			onMessageCtx.declared["message"] = true
			g.emitArrowStmts(&b, ep.OnMessage, "    ", onMessageCtx)
		}
		b.WriteString("  },\n")

		// onClose handler
		b.WriteString("  onClose(event, ws) {\n")
		if len(ep.OnDisconnect) > 0 {
			onDisconnectCtx := ctx
			onDisconnectCtx.declared = make(map[string]bool)
			g.emitArrowStmts(&b, ep.OnDisconnect, "    ", onDisconnectCtx)
		}
		b.WriteString("  },\n")

		b.WriteString("}))));\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(fileKey)),
		Content: []byte(b.String()),
	}
}

// --- Subscriptions ---

// genEventsLib generates the shared event subscription registry (src/lib/events.ts).
func (g *Generator) genEventsLib() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`// Event subscription registry — wire up in src/index.ts
export type EventHandler = (event: unknown) => Promise<void>;
export const subscriptions: Record<string, EventHandler[]> = {};
export function on(event: string, handler: EventHandler) {
  if (!subscriptions[event]) subscriptions[event] = [];
  subscriptions[event].push(handler);
}
export async function emit(event: string, data: unknown) {
  for (const handler of subscriptions[event] ?? []) {
    await handler(data);
  }
}
`)
	return codegen.OutputFile{Path: "src/lib/events.ts", Content: []byte(b.String())}
}

// genSubscribe generates an event subscription handler file (src/subscriptions/<name>.ts).
func (g *Generator) genSubscribe(sub *ast.Subscribe) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Collect imports from subscription body
	ic := g.collectImports(sub.Stmts)

	// Add data imports if logic uses data operations
	if stmtsHaveDataOps(sub.Stmts) {
		writeDataImports(&b)
	}

	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	// Handler function name: "on" + PascalCase(event)
	handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))

	if sub.Intent != nil {
		b.WriteString(fmt.Sprintf("// %s\n", sub.Intent.Text))
	}
	b.WriteString(fmt.Sprintf("export async function %s(event: unknown): Promise<void> {\n", handlerName))

	ctx := emitCtx{
		kind:        "function",
		declared:    make(map[string]bool),
		asyncFns:    g.buildAsyncFns(),
		structEnums: g.structEnums,
	}
	g.emitArrowStmts(&b, sub.Stmts, "  ", ctx)
	b.WriteString("}\n")

	// Build file name from event name (replace dots with hyphens)
	fileName := toKebabCase(strings.ReplaceAll(sub.Event, ".", "-"))
	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/subscriptions/%s.ts", fileName),
		Content: []byte(b.String()),
	}
}

// --- External ---

func (g *Generator) genExternal(externals []*ast.External) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { env } from './env.js';\n\n")

	for _, ext := range externals {
		rawName := strings.Trim(ext.Name, `"`)
		normalized := normalizeServiceName(rawName)
		configName := toCamelCase(normalized)
		pascalName := toPascalCase(normalized)
		b.WriteString(fmt.Sprintf("// External service: %s\n", ext.Name))
		b.WriteString(fmt.Sprintf("export const %s = {\n", configName))
		for _, kv := range ext.Entries {
			b.WriteString(fmt.Sprintf("  %s: %s,\n", toCamelCase(kv.Key), exprToJS(kv.Value)))
		}
		b.WriteString("};\n\n")

		// Generate the call helper function for this external service.
		// call<PascalName>(method, path, body?) wraps fetch with the service config.
		b.WriteString(fmt.Sprintf("export async function call%s(\n", pascalName))
		b.WriteString("  method: string,\n")
		b.WriteString("  path: string,\n")
		b.WriteString("  body?: unknown,\n")
		b.WriteString("): Promise<any> {\n")
		b.WriteString(fmt.Sprintf("  const baseUrl = (%s as any).url ?? (%s as any).base ?? '';\n", configName, configName))
		b.WriteString(fmt.Sprintf("  const timeout = (%s as any).timeout ?? 30000;\n", configName))
		b.WriteString("  const controller = new AbortController();\n")
		b.WriteString("  const timer = setTimeout(() => controller.abort(), timeout);\n")
		b.WriteString("  try {\n")
		b.WriteString("    const res = await fetch(`${baseUrl}${path}`, {\n")
		b.WriteString("      method,\n")
		b.WriteString("      signal: controller.signal,\n")
		b.WriteString("      ...(body !== undefined ? {\n")
		b.WriteString("        body: JSON.stringify(body),\n")
		b.WriteString("        headers: { 'Content-Type': 'application/json' },\n")
		b.WriteString("      } : {}),\n")
		b.WriteString("    });\n")
		b.WriteString("    if (!res.ok) throw new Error(`External call failed: ${res.status} ${res.statusText}`);\n")
		b.WriteString("    const ct = res.headers.get('content-type') ?? '';\n")
		b.WriteString("    if (ct.includes('application/json')) return res.json();\n")
		b.WriteString("    return res.text();\n")
		b.WriteString("  } finally {\n")
		b.WriteString("    clearTimeout(timer);\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/lib/external.ts", Content: []byte(b.String())}
}

// --- Dockerfile ---

func (g *Generator) genDockerfile() codegen.OutputFile {
	content := `FROM node:22-slim AS base
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --production

COPY . .

EXPOSE 3000
CMD ["npx", "tsx", "src/index.ts"]
`
	return codegen.OutputFile{Path: "Dockerfile", Content: []byte(content)}
}

// --- Tests ---

func (g *Generator) genTest(t *ast.Test, fixtures []*ast.Fixture) codegen.OutputFile {
	var b strings.Builder

	// Build fixture lookup map.
	fixtureMap := make(map[string]*ast.Fixture)
	for _, f := range fixtures {
		fixtureMap[f.Name] = f
	}

	// Collect setup bindings that need hoisting to describe scope so they're
	// accessible in both beforeAll and the it() body.
	hoistedVarNames := make(map[string]bool)
	var hoistedVarList []string
	for _, stmt := range t.Setup {
		if step, ok := stmt.(*ast.StepStmt); ok && step.Binding != "" {
			name := toCamelCase(step.Binding)
			if !hoistedVarNames[name] {
				hoistedVarNames[name] = true
				hoistedVarList = append(hoistedVarList, name)
			}
		}
	}

	// Detect if any from-path fixtures are referenced in the request body.
	needsFSImport := false
	if t.Request != nil {
		for _, kv := range t.Request.Entries {
			if kv.Key == "body" && testExprUsesFixture(kv.Value, fixtureMap, true) {
				needsFSImport = true
			}
		}
	}

	// Check assertion kinds so we know whether to parse the response body.
	hasBodyAssertions := false
	needsDbImport := false
	for _, a := range t.Expect {
		if a.Kind == "body" {
			hasBodyAssertions = true
		}
		if a.Kind == "model" {
			needsDbImport = true
		}
	}

	// Imports
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { describe, it, expect, beforeAll } from 'vitest';\n")
	if needsFSImport {
		b.WriteString("import { readFileSync } from 'fs';\n")
		b.WriteString("import { join } from 'path';\n")
	}
	if needsDbImport {
		b.WriteString("import { db } from '../src/lib/db.js';\n")
		b.WriteString("import * as schema from '../src/models/schema.js';\n")
		b.WriteString("import { eq, and } from 'drizzle-orm';\n")
	}
	b.WriteString("import app from '../src/index.js';\n\n")

	name := toCamelCase(t.Name)
	if t.Intent != nil {
		b.WriteString(fmt.Sprintf("// %s\n", t.Intent.Text))
	}
	b.WriteString(fmt.Sprintf("describe('%s', () => {\n", name))

	// Hoist setup variables to describe scope.
	for _, v := range hoistedVarList {
		b.WriteString(fmt.Sprintf("  let %s: any;\n", v))
	}
	if len(hoistedVarList) > 0 {
		b.WriteString("\n")
	}

	// Setup — pre-populate ctx.declared with hoisted names so emitArrowStmts
	// emits plain assignments instead of const declarations.
	if len(t.Setup) > 0 {
		ctx := emitCtx{
			kind:       "function",
			declared:   hoistedVarNames,
			boundVars:  make(map[string]string),
			varModels:  make(map[string]string),
			singleVars: make(map[string]bool),
		}
		b.WriteString("  beforeAll(async () => {\n")
		g.emitArrowStmts(&b, t.Setup, "    ", ctx)
		b.WriteString("  });\n\n")
	}

	// Repeat wrapper.
	repeat := 1
	if t.Request != nil && t.Request.Repeat > 1 {
		repeat = t.Request.Repeat
	}
	b.WriteString(fmt.Sprintf("  it('%s', async () => {\n", t.Name))
	indent := "    "
	if repeat > 1 {
		b.WriteString(fmt.Sprintf("    for (let _i = 0; _i < %d; _i++) {\n", repeat))
		indent = "      "
	}

	// HTTP request.
	if t.Target != nil {
		method := strings.ToUpper(t.Target.Method)
		b.WriteString(fmt.Sprintf("%sconst res = await app.request('%s', {\n", indent, t.Target.Path))
		b.WriteString(fmt.Sprintf("%s  method: '%s',\n", indent, method))

		if t.Request != nil {
			// Auth header (e.g. auth api_key(key.key_hash) → X-API-Key header).
			for _, kv := range t.Request.Entries {
				if kv.Key == "auth" {
					if hdr := testAuthHeader(kv.Value); hdr != "" {
						b.WriteString(fmt.Sprintf("%s  headers: %s,\n", indent, hdr))
					}
				}
			}
			// Request body with fixture() resolution.
			for _, kv := range t.Request.Entries {
				if kv.Key == "body" {
					b.WriteString(fmt.Sprintf("%s  body: JSON.stringify(%s),\n", indent, testResolveExpr(kv.Value, fixtureMap)))
				}
			}
		}
		b.WriteString(fmt.Sprintf("%s});\n", indent))
	}

	// Parse response body once if any body assertions exist.
	if hasBodyAssertions {
		b.WriteString(fmt.Sprintf("%sconst body = await res.json() as any;\n", indent))
	}

	// Assertions.
	for _, a := range t.Expect {
		emitTestAssertion(&b, a, indent)
	}

	if repeat > 1 {
		b.WriteString("    }\n")
	}
	b.WriteString("  });\n")
	b.WriteString("});\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("test/%s.test.ts", toKebabCase(t.Name)),
		Content: []byte(b.String()),
	}
}

// testExprUsesFixture reports whether expr contains a fixture() call that
// references a from-path fixture (requiresFSImport=true) or any fixture.
func testExprUsesFixture(expr ast.Expr, fixtures map[string]*ast.Fixture, requiresFSImport bool) bool {
	switch v := expr.(type) {
	case *ast.FnCall:
		if v.Name == "fixture" && len(v.Args) == 1 {
			if s, ok := v.Args[0].(*ast.StringLit); ok {
				if f, ok := fixtures[s.Value]; ok {
					if requiresFSImport {
						return f.FromPath != ""
					}
					return true
				}
			}
			return !requiresFSImport
		}
		for _, a := range v.Args {
			if testExprUsesFixture(a, fixtures, requiresFSImport) {
				return true
			}
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			if testExprUsesFixture(kv.Value, fixtures, requiresFSImport) {
				return true
			}
		}
	}
	return false
}

// testResolveExpr converts an expression to JS, resolving fixture() calls.
func testResolveExpr(expr ast.Expr, fixtures map[string]*ast.Fixture) string {
	switch v := expr.(type) {
	case *ast.FnCall:
		if v.Name == "fixture" && len(v.Args) == 1 {
			if s, ok := v.Args[0].(*ast.StringLit); ok {
				if f, ok := fixtures[s.Value]; ok {
					if f.FromPath != "" {
						return fmt.Sprintf("readFileSync(join(__dirname, '..', '%s'))", f.FromPath)
					}
					if f.Generated != nil {
						// Extract size if present (e.g. size: 15mb → Buffer.alloc(15 * 1024 * 1024)).
						for _, kv := range f.Generated.Entries {
							if kv.Key == "size" {
								sizeExpr := exprToJS(kv.Value)
								return fmt.Sprintf("((_s) => { if (_s <= 0) throw new RangeError('Buffer size must be positive'); return Buffer.alloc(_s); })(%s)", sizeExpr)
							}
						}
						return "Buffer.alloc(0)"
					}
				}
				return fmt.Sprintf("null /* fixture %q not found */", s.Value)
			}
		}
		// Regular function call — resolve args recursively.
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = testResolveExpr(a, fixtures)
		}
		return fmt.Sprintf("%s(%s)", toCamelCase(v.Name), strings.Join(args, ", "))
	case *ast.BlockExpr:
		parts := make([]string, 0, len(v.Entries))
		for _, kv := range v.Entries {
			parts = append(parts, fmt.Sprintf("%s: %s", kv.Key, testResolveExpr(kv.Value, fixtures)))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		return exprToJS(expr)
	}
}

// testAuthHeader generates a JS headers object for a test auth entry.
// e.g. auth api_key(key.key_hash) → { 'X-API-Key': key.keyHash }
func testAuthHeader(authExpr ast.Expr) string {
	fn, ok := authExpr.(*ast.FnCall)
	if !ok {
		return ""
	}
	argJS := ""
	if len(fn.Args) > 0 {
		argJS = exprToJS(fn.Args[0])
	}
	switch fn.Name {
	case "api_key":
		return fmt.Sprintf("{ 'X-API-Key': %s }", argJS)
	case "bearer", "jwt":
		return fmt.Sprintf("{ 'Authorization': `Bearer ${%s}` }", argJS)
	case "basic":
		return fmt.Sprintf("{ 'Authorization': `Basic ${%s}` }", argJS)
	}
	return ""
}

// tokenizeAssertion splits an assertion raw string into tokens, keeping quoted
// strings (including those with spaces) as single tokens. This prevents fragile
// whitespace-based splitting from breaking assertions like: status == "hello world"
func tokenizeAssertion(raw string) []string {
	var tokens []string
	i := 0
	for i < len(raw) {
		// Skip whitespace
		if raw[i] == ' ' || raw[i] == '\t' {
			i++
			continue
		}
		// Quoted string — collect until closing quote
		if raw[i] == '"' {
			j := i + 1
			for j < len(raw) && raw[j] != '"' {
				if raw[j] == '\\' && j+1 < len(raw) {
					j++ // skip escaped char
				}
				j++
			}
			if j < len(raw) {
				j++ // include closing quote
			}
			tokens = append(tokens, raw[i:j])
			i = j
			continue
		}
		// Regular token — collect until whitespace
		j := i
		for j < len(raw) && raw[j] != ' ' && raw[j] != '\t' {
			j++
		}
		tokens = append(tokens, raw[i:j])
		i = j
	}
	return tokens
}

// emitTestAssertion emits a single expect() call for a test assertion.
func emitTestAssertion(b *strings.Builder, a *ast.Assertion, indent string) {
	fields := tokenizeAssertion(a.Raw)
	switch a.Kind {
	case "status":
		// "status 200" — second field is the numeric code.
		if len(fields) >= 2 {
			b.WriteString(fmt.Sprintf("%sexpect(res.status).toBe(%s);\n", indent, fields[1]))
		}
	case "body":
		// Patterns: body . field is type  /  body . field == value  /  body . field exists
		path, op, rhs := parseAssertionFields(fields)
		if path == "" {
			b.WriteString(fmt.Sprintf("%s// TODO: assert %s\n", indent, a.Raw))
			return
		}
		// Convert snake_case field to camelCase JS path.
		parts := strings.SplitN(path, ".", 2)
		var jsPath string
		if len(parts) == 2 {
			jsPath = parts[0] + "." + toCamelCase(parts[1])
		} else {
			jsPath = path
		}
		switch op {
		case "is":
			emitTypeExpect(b, jsPath, rhs, indent)
		case "==":
			b.WriteString(fmt.Sprintf("%sexpect(%s).toBe(%s);\n", indent, jsPath, rhs))
		case "!=":
			b.WriteString(fmt.Sprintf("%sexpect(%s).not.toBe(%s);\n", indent, jsPath, rhs))
		case "exists":
			b.WriteString(fmt.Sprintf("%sexpect(%s).toBeDefined();\n", indent, jsPath))
		case "not_exists":
			b.WriteString(fmt.Sprintf("%sexpect(%s).toBeUndefined();\n", indent, jsPath))
		default:
			b.WriteString(fmt.Sprintf("%s// TODO: assert %s\n", indent, a.Raw))
		}
	case "header":
		// header . Name == value
		path, op, rhs := parseAssertionFields(fields)
		if path != "" && op == "==" {
			parts := strings.SplitN(path, ".", 2)
			headerName := ""
			if len(parts) == 2 {
				headerName = parts[1]
			}
			b.WriteString(fmt.Sprintf("%sexpect(res.headers.get('%s')).toBe(%s);\n", indent, headerName, rhs))
		} else {
			b.WriteString(fmt.Sprintf("%s// TODO: assert %s\n", indent, a.Raw))
		}
	case "model":
		// model job where ( id == body . job_id , status == "done" ) exists
		emitModelAssertion(b, fields, indent)
	case "duration":
		// duration < 500ms  — timing assertion (approximate)
		b.WriteString(fmt.Sprintf("%s// TODO: assert %s\n", indent, a.Raw))
	default:
		b.WriteString(fmt.Sprintf("%s// TODO: assert %s\n", indent, a.Raw))
	}
}

// emitModelAssertion emits a db query assertion for model existence checks.
// Raw format: "model job where ( id == body . job_id , status == \"done\" ) exists"
// On parse failure, emits expect(true).toBe(false) so tests fail loudly.
func emitModelAssertion(b *strings.Builder, fields []string, indent string) {
	raw := strings.Join(fields, " ")

	// fields[0] == "model", fields[1] == model name
	if len(fields) < 2 {
		b.WriteString(fmt.Sprintf("%sexpect(true).toBe(false); // PARSE ERROR: could not parse model assertion: %s\n", indent, raw))
		return
	}
	modelName := fields[1]
	tableName := pluralize(modelName)

	// Find where-clause: scan for "(" and ")"
	start := -1
	end_ := -1
	for i, f := range fields {
		if f == "(" && start == -1 {
			start = i + 1
		}
		if f == ")" {
			end_ = i
		}
	}

	// Validate: must have matching parens with content
	if start < 0 || end_ < 0 || end_ <= start {
		b.WriteString(fmt.Sprintf("%sexpect(true).toBe(false); // PARSE ERROR: missing or empty where clause in model assertion: %s\n", indent, raw))
		return
	}

	// Determine exists/not_exists — last meaningful token after ")"
	exists := true
	tailValid := false
	if end_+1 < len(fields) {
		tail := fields[end_+1]
		if tail == "exists" {
			tailValid = true
		} else if tail == "not" {
			exists = false
			tailValid = true
		}
	}
	if !tailValid {
		b.WriteString(fmt.Sprintf("%sexpect(true).toBe(false); // PARSE ERROR: missing exists/not after where in model assertion: %s\n", indent, raw))
		return
	}

	// Parse conditions between ( and )
	condFields := fields[start:end_]
	var conditions []string
	var parseErrors []string
	var current []string
	for _, f := range condFields {
		if f == "," {
			cond := parseModelCondition(current, tableName)
			if cond == "" {
				parseErrors = append(parseErrors, strings.Join(current, " "))
			} else {
				conditions = append(conditions, cond)
			}
			current = nil
		} else {
			current = append(current, f)
		}
	}
	if len(current) > 0 {
		cond := parseModelCondition(current, tableName)
		if cond == "" {
			parseErrors = append(parseErrors, strings.Join(current, " "))
		} else {
			conditions = append(conditions, cond)
		}
	}

	// If any conditions failed to parse, emit a failing assertion
	if len(parseErrors) > 0 {
		b.WriteString(fmt.Sprintf("%sexpect(true).toBe(false); // PARSE ERROR: could not parse model condition(s): %s\n", indent, strings.Join(parseErrors, "; ")))
		return
	}

	// Must have at least one condition
	if len(conditions) == 0 {
		b.WriteString(fmt.Sprintf("%sexpect(true).toBe(false); // PARSE ERROR: no conditions found in model assertion: %s\n", indent, raw))
		return
	}

	// Emit the query
	schemaTable := "schema." + toCamelCase(tableName)
	b.WriteString(fmt.Sprintf("%sconst _row = await db.select().from(%s)", indent, schemaTable))
	if len(conditions) == 1 {
		b.WriteString(fmt.Sprintf(".where(%s)", conditions[0]))
	} else if len(conditions) > 1 {
		b.WriteString(fmt.Sprintf(".where(and(\n"))
		for i, c := range conditions {
			sep := ","
			if i == len(conditions)-1 {
				sep = ""
			}
			b.WriteString(fmt.Sprintf("%s  %s%s\n", indent, c, sep))
		}
		b.WriteString(fmt.Sprintf("%s))", indent))
	}
	b.WriteString(".limit(1);\n")
	if exists {
		b.WriteString(fmt.Sprintf("%sexpect(_row.length).toBeGreaterThan(0);\n", indent))
	} else {
		b.WriteString(fmt.Sprintf("%sexpect(_row.length).toBe(0);\n", indent))
	}
}

// parseModelCondition converts a condition token slice into a Drizzle eq() call.
// e.g. ["id", "==", "body", ".", "job_id"] → `eq(schema.jobs.id, body.jobId)`
// Returns an error comment if the condition cannot be parsed, so tests fail visibly
// instead of silently dropping assertions.
func parseModelCondition(tokens []string, tableName string) string {
	if len(tokens) < 3 {
		return fmt.Sprintf("/* ERROR: malformed condition, expected at least 3 tokens, got %d: %s */", len(tokens), strings.Join(tokens, " "))
	}
	// Find operator
	opIdx := -1
	for i, t := range tokens {
		if t == "==" || t == "!=" {
			opIdx = i
			break
		}
	}
	if opIdx < 0 {
		return fmt.Sprintf("/* ERROR: no operator found in condition: %s */", strings.Join(tokens, " "))
	}

	// LHS: join tokens before op, strip dots → field name
	lhsParts := make([]string, 0)
	for _, t := range tokens[:opIdx] {
		if t != "." {
			lhsParts = append(lhsParts, t)
		}
	}
	// RHS: join tokens after op
	rhsParts := make([]string, 0)
	for _, t := range tokens[opIdx+1:] {
		if t != "." {
			rhsParts = append(rhsParts, t)
		}
	}

	if len(lhsParts) == 0 || len(rhsParts) == 0 {
		return fmt.Sprintf("/* ERROR: empty LHS or RHS in condition: %s */", strings.Join(tokens, " "))
	}

	lhsField := toCamelCase(lhsParts[len(lhsParts)-1])
	schemaTable := "schema." + toCamelCase(tableName)
	lhsJS := schemaTable + "." + lhsField

	// Build RHS JS expression
	var rhs string
	if len(rhsParts) == 1 {
		v := rhsParts[0]
		// Quoted string → single-quote
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			rhs = "'" + v[1:len(v)-1] + "'"
		} else {
			rhs = v
		}
	} else {
		// Multi-part: e.g. body job_id → body.jobId
		last := toCamelCase(rhsParts[len(rhsParts)-1])
		rhs = rhsParts[0] + "." + last
	}

	op := tokens[opIdx]
	if op == "==" {
		return fmt.Sprintf("eq(%s, %s)", lhsJS, rhs)
	}
	return fmt.Sprintf("ne(%s, %s)", lhsJS, rhs)
}

// parseAssertionFields parses token fields from a Raw assertion string.
// Returns the dotted path, operator (is/==/!=/exists/not_exists), and rhs value.
func parseAssertionFields(fields []string) (path, op, rhs string) {
	var pathParts []string
	i := 0
	for i < len(fields) {
		f := fields[i]
		if f == "." {
			i++
			continue
		}
		if f == "is" || f == "==" || f == "!=" || f == "exists" || f == "not" {
			break
		}
		pathParts = append(pathParts, f)
		i++
	}
	if len(pathParts) == 0 || i >= len(fields) {
		return "", "", ""
	}
	path = strings.Join(pathParts, ".")
	op = fields[i]
	if op == "exists" {
		return path, "exists", ""
	}
	if op == "not" && i+1 < len(fields) && fields[i+1] == "exists" {
		return path, "not_exists", ""
	}
	if i+1 < len(fields) {
		v := fields[i+1]
		// Quoted string token (e.g. "done") → convert to JS single-quote string.
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			rhs = "'" + v[1:len(v)-1] + "'"
		} else {
			rhs = v
		}
	}
	return path, op, rhs
}

// emitTypeExpect emits a type assertion for test output.
func emitTypeExpect(b *strings.Builder, jsPath, typeName, indent string) {
	switch typeName {
	case "string":
		b.WriteString(fmt.Sprintf("%sexpect(typeof %s).toBe('string');\n", indent, jsPath))
	case "uuid":
		b.WriteString(fmt.Sprintf("%sexpect(%s).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);\n", indent, jsPath))
	case "email":
		b.WriteString(fmt.Sprintf(`%sexpect(%s).toMatch(/^[^\s@]+@[^\s@]+\.[^\s@]+$/);`+"\n", indent, jsPath))
	case "url":
		b.WriteString(fmt.Sprintf("%sexpect(() => new URL(%s)).not.toThrow();\n", indent, jsPath))
	case "number", "int", "float":
		b.WriteString(fmt.Sprintf("%sexpect(typeof %s).toBe('number');\n", indent, jsPath))
	case "bool", "boolean":
		b.WriteString(fmt.Sprintf("%sexpect(typeof %s).toBe('boolean');\n", indent, jsPath))
	default:
		b.WriteString(fmt.Sprintf("%sexpect(%s).toBeDefined(); // type: %s\n", indent, jsPath, typeName))
	}
}

// webhookAuthSecretKey extracts the env variable name from a webhook_sig auth expression.
// Input AST: FnCall{Name:"webhook_sig", Args:[FnCall{Name:"using", Args:[FieldAccess{Base:"secret",Field:"KEY"}]}]}
// Returns the field name (e.g. "STRIPE_KEY"), or "" if not a webhook_sig auth.
func webhookAuthSecretKey(expr ast.Expr) string {
	fn, ok := expr.(*ast.FnCall)
	if !ok || fn.Name != "webhook_sig" || len(fn.Args) == 0 {
		return ""
	}
	inner, ok := fn.Args[0].(*ast.FnCall)
	if !ok || inner.Name != "using" || len(inner.Args) == 0 {
		return ""
	}
	fa, ok := inner.Args[0].(*ast.FieldAccess)
	if !ok {
		return ""
	}
	return fa.Field
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
