package importer

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/js"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestImportTypeScriptStructuralScaffold(t *testing.T) {
	sources := []Source{
		{Path: "src/schema.ts", Data: []byte(`
import { pgTable, uuid, varchar, integer, timestamp } from "drizzle-orm/pg-core";

export const postStatus = pgEnum("post_status", ["draft", "published"]);

export const users = pgTable("users", {
  id: uuid("id").primaryKey().defaultRandom(),
  email: varchar("email", { length: 255 }).notNull().unique(),
  age: integer("age"),
  createdAt: timestamp("created_at").notNull().defaultNow(),
});

export const posts = pgTable("posts", {
  id: uuid("id").primaryKey().defaultRandom(),
  authorId: uuid("author_id").notNull().references(() => users.id),
  title: varchar("title", { length: 200 }).notNull(),
  status: postStatus("status").notNull(),
});
`)},
		{Path: "src/app.ts", Data: []byte(`
const CreatePost = z.object({
  title: z.string().min(1).max(200),
  score: z.number().int().optional(),
  published: z.boolean().default(false),
});

app.post("/posts", zValidator("json", CreatePost), async (c) => {
  await db.insert(posts).values(c.req.valid("json"));
  return c.json({ ok: true }, 201);
});

app.get('/posts/:postId', async (c) => c.json(await loadPost(c.req.param('postId'))));
`)},
	}

	result, err := ImportTypeScript(sources, Options{Name: "Blog API"})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	for _, want := range []string{
		`blueprint "blog_api"`,
		"database postgres",
		"model post {",
		"author_id",
		"ref(user)",
		"status    enum(draft, published) required",
		"model user {",
		"unique required",
		"POST /posts {",
		"<- title      string  required  min(1)  max(200)",
		"<- score      int     optional",
		"<- published  bool    default(false)",
		"GET /posts/:post_id {",
		"<- post_id  string  required",
		`|> @ "TODO(import): rewrite the original POST /posts handler; imperative behavior was not preserved."`,
		`-> 501 {`,
	} {
		if !strings.Contains(result.Source, want) {
			t.Errorf("scaffold missing %q:\n%s", want, result.Source)
		}
	}
	if result.Report.Models != 2 || result.Report.Routes != 2 || result.Report.Inputs != 4 {
		t.Fatalf("unexpected report counts: %+v", result.Report)
	}
	if len(result.Report.Handlers) != 2 || len(result.Report.Handlers[0].Dropped) == 0 {
		t.Fatalf("expected per-handler dropped behavior report, got %+v", result.Report.Handlers)
	}

	parsed, parseErrs := parser.ParseFile("imported.bp", []byte(result.Source))
	if len(parseErrs) != 0 {
		t.Fatalf("generated scaffold does not parse: %+v", parseErrs)
	}
	if checkErrs := checker.Check(parsed); len(checkErrs) != 0 {
		t.Fatalf("generated scaffold does not check: %+v", checkErrs)
	}
	if _, err := js.New().Files(parsed); err != nil {
		t.Fatalf("generated scaffold does not reach Node codegen: %v", err)
	}
}

func TestImportTypeScriptDropsUnknownRefLoudly(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "schema.ts", Data: []byte(`
export const posts = pgTable("posts", {
  id: uuid("id").primaryKey(),
  authorId: uuid("author_id").references(() => users.id),
});
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if strings.Contains(result.Source, "ref(user)") {
		t.Fatalf("unavailable reference must not make the scaffold invalid:\n%s", result.Source)
	}
	joined := strings.Join(result.Report.Warnings, "\n")
	if !strings.Contains(joined, "dropped unverifiable ref") {
		t.Fatalf("missing loud unknown-ref warning: %v", result.Report.Warnings)
	}
}

func TestImportTypeScriptMapsSupportedZodBoundaryTypes(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
const Payload = z.object({
  id: z.string().uuid(),
  happenedAt: z.string().datetime(),
  email: z.string().email(),
  tags: z.array(z.string()).optional(),
  metadata: z.record(z.string()),
});
app.post("/events", zValidator("json", Payload), handler);
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	for _, want := range []string{
		"<- id           uuid",
		"<- happened_at  timestamp",
		"<- email        string",
		"format(email)",
		"<- tags_value   list(string)  optional",
		"<- metadata     json",
	} {
		if !strings.Contains(result.Source, want) {
			t.Errorf("mapped Zod scaffold missing %q:\n%s", want, result.Source)
		}
	}
	if !strings.Contains(strings.Join(result.Report.Warnings, "\n"), `renamed identifier "tags" to "tags_value"`) {
		t.Fatalf("reserved input rename was not disclosed: %v", result.Report.Warnings)
	}
}

func TestImportTypeScriptDuplicateRouteIsNotSilentlyMerged(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
app.get("/health", (c) => c.text("one"));
router.get("/health", (c) => c.text("two"));
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if result.Report.Routes != 1 || result.Report.SkippedRoutes != 1 || len(result.Report.Handlers) != 2 || !result.Report.Handlers[1].DuplicateKey {
		t.Fatalf("duplicate route was not represented honestly: %+v", result.Report)
	}
	if !strings.Contains(strings.Join(result.Report.Warnings, "\n"), "duplicate route GET /health skipped") {
		t.Fatalf("duplicate route warning missing: %v", result.Report.Warnings)
	}
}

func TestImportTypeScriptAppliesStaticHonoBasePathAndWarnsOnMounts(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
const api = new Hono().basePath("/api/v1");
api.get("/users/:userId", handler).post("/users", handler);
app.route("/admin", adminRoutes);
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if !strings.Contains(result.Source, "GET /api/v1/users/:user_id") {
		t.Fatalf("static basePath was not applied:\n%s", result.Source)
	}
	if !strings.Contains(result.Source, "POST /api/v1/users") {
		t.Fatalf("static basePath was not retained across a chained route:\n%s", result.Source)
	}
	if !strings.Contains(strings.Join(result.Report.Handlers[0].Mapped, " "), "Hono basePath") {
		t.Fatalf("basePath was not disclosed in mapped facts: %+v", result.Report.Handlers[0])
	}
	if !strings.Contains(strings.Join(result.Report.Warnings, "\n"), "route mount") {
		t.Fatalf("unflattened route mount warning missing: %v", result.Report.Warnings)
	}
}

func TestImportTypeScriptRequiresRecognizedStructure(t *testing.T) {
	_, err := ImportTypeScript([]Source{{Path: "plain.ts", Data: []byte(`export const answer = 42`)}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "no supported structure") {
		t.Fatalf("expected actionable unsupported input error, got %v", err)
	}
}

func TestImportTypeScriptSkipsUnrepresentableRoutesLoudly(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(
		"app.get(\"/ok\", handler);\n" +
			"app.head(\"/ok\", handler);\n" +
			"app.get(`/files/${kind}`, handler);\n" +
			"app.get(\"/files/*\", handler);\n")}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if result.Report.Routes != 1 || result.Report.SkippedRoutes != 3 {
		t.Fatalf("unexpected supported/skipped route accounting: %+v", result.Report)
	}
	if strings.Contains(result.Source, "HEAD ") || strings.Contains(result.Source, "/files") {
		t.Fatalf("unrepresentable route leaked into the scaffold:\n%s", result.Source)
	}
	joined := strings.Join(result.Report.Warnings, "\n")
	for _, want := range []string{"HEAD", "dynamic/non-path", "wildcard"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q warning:\n%s", want, joined)
		}
	}
}

func TestImportTypeScriptIgnoresCommentedRoutes(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
// app.get("/commented", handler)
/* router.post("/also-commented", handler) */
const example = "app.delete('/inside-string', handler)";
app.get("/real", handler).post("/chained", handler)
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if result.Report.Routes != 2 || !strings.Contains(result.Source, "GET /real") || !strings.Contains(result.Source, "POST /chained") || strings.Contains(result.Source, "/commented") || strings.Contains(result.Source, "/inside-string") {
		t.Fatalf("comment handling produced the wrong routes:\n%s", result.Source)
	}
}

func TestImportTypeScriptPreservesOnlyRepresentableValidatorTransports(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
const QueryInput = z.object({ q: z.string().min(1) });
const Params = z.object({ postId: z.string().uuid() });
const Body = z.object({ title: z.string().min(1) });
const MentionedOnly = z.object({ unsafe: z.string() });

app.post("/search", zValidator("query", QueryInput), handler);
app.post("/posts/:postId", zValidator("param", Params), zValidator("json", Body), handler);
app.post("/mentioned", arbitraryMiddleware(MentionedOnly), handler);
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if strings.Contains(result.Source, "<- q") || strings.Contains(result.Source, "<- unsafe") {
		t.Fatalf("unrepresentable or unverified schemas leaked into route inputs:\n%s", result.Source)
	}
	for _, want := range []string{
		"POST /posts/:post_id",
		"<- post_id  uuid",
		"<- title    string",
	} {
		if !strings.Contains(result.Source, want) {
			t.Errorf("transport-aware scaffold missing %q:\n%s", want, result.Source)
		}
	}
	warnings := strings.Join(result.Report.Warnings, "\n")
	for _, want := range []string{
		"cannot preserve this transport",
		"mentioned outside a recognized zValidator",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing transport warning %q:\n%s", want, warnings)
		}
	}
}

func TestImportTypeScriptReportsDroppedZodSemantics(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "app.ts", Data: []byte(`
const Payload = z.object({
  transformed: z.string().transform((value) => value.length),
  nullableName: z.string().nullable(),
  slug: z.string().regex(/^[a-z]+$/),
  website: z.string().url(),
  tags: z.array(z.string().min(1)).max(3),
});
app.post("/items", zValidator("json", Payload), handler);
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if strings.Contains(result.Source, "<- transformed") || strings.Contains(result.Source, "<- nullable_name") {
		t.Fatalf("type-changing/nullable fields must be skipped:\n%s", result.Source)
	}
	for _, want := range []string{"<- slug", "<- website", "format(url)", "<- tags", "max(3)"} {
		if !strings.Contains(result.Source, want) {
			t.Errorf("Zod scaffold missing %q:\n%s", want, result.Source)
		}
	}
	warnings := strings.Join(result.Report.Warnings, "\n")
	for _, want := range []string{"type-changing transform", "nullable/nullish", "dropped Zod regex()", "array-element constraints"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing Zod-loss warning %q:\n%s", want, warnings)
		}
	}
}

func TestImportTypeScriptUsesStaticSQLColumnNameAndWarnsOnOptions(t *testing.T) {
	result, err := ImportTypeScript([]Source{{Path: "schema.ts", Data: []byte(`
export const users = pgTable("users", {
  id: uuid("id").primaryKey(),
  email: varchar("email").notNull().unique(),
});
export const ledgers = pgTable("ledgers", {
  id: uuid("id").primaryKey(),
  legacy: varchar("legacy_name", { length: 32 }).notNull(),
  amount: numeric("amount", { precision: 12, scale: 2 }).notNull(),
  owner_email: varchar("owner_email").references(() => users.email),
});
`)}}, Options{})
	if err != nil {
		t.Fatalf("ImportTypeScript failed: %v", err)
	}
	if !strings.Contains(result.Source, "legacy_name") || strings.Contains(result.Source, "\n  legacy ") {
		t.Fatalf("static SQL column identity was not preserved:\n%s", result.Source)
	}
	if strings.Contains(result.Source, "owner_email string optional ref(user)") {
		t.Fatalf("a non-id Drizzle reference must not be rewritten as a Blueprint id ref:\n%s", result.Source)
	}
	warnings := strings.Join(result.Report.Warnings, "\n")
	for _, want := range []string{"used SQL column name", "dropped Drizzle builder options", "verify range and precision", "dropped reference to users.email"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing Drizzle-loss warning %q:\n%s", want, warnings)
		}
	}
}
