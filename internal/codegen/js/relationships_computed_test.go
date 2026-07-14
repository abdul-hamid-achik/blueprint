package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const relationshipsComputedSource = `blueprint "relationships" {
  version "1.0"
  runtime node
  database postgres
}
model author {
  id uuid primary
  name string required
  computed display_name string = name + "!"
}
model post {
  id uuid primary
  author_id uuid required ref(author)
  title string required
  computed headline string = title + "!"
}
GET /api/posts {
  |> posts = query post with(author) order(title, asc)
  -> 200 { posts: posts }
}
GET /api/latest-post {
  |> latest = query post with(author) first
  -> 200 { post: latest }
}
GET /api/post-page {
  <- page int default(1)
  <- per_page int default(20)
  |> result = query post with(author) paginate(page, per_page)
  -> 200 { result: result }
}
GET /api/authors/:id {
  <- id uuid required
  |> author = fetch author(id)
  -> 200 { author: author }
}
GET /api/post-author/:id {
  <- id uuid required
  |> post = fetch post(id)
  -> 200 { display_name: post.author.display_name }
}
POST /api/posts {
  <- author_id uuid required
  <- title string required
  |> post = save post { author_id: author_id, title: title }
  -> 201 { post: post }
}`

func generateRelationshipsComputed(t *testing.T) string {
	t.Helper()
	file, parseErrors := parser.ParseFile("relationships.bp", []byte(relationshipsComputedSource))
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %v", parseErrors)
	}
	if errors := checker.Check(file); len(errors) > 0 {
		t.Fatalf("check errors: %v", errors)
	}
	dir := t.TempDir()
	if err := New().Generate(file, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return dir
}

func TestGenerateComputedFieldsAndRelationshipJoin(t *testing.T) {
	dir := generateRelationshipsComputed(t)
	schemaBytes, err := os.ReadFile(filepath.Join(dir, "src/models/schema.ts"))
	if err != nil {
		t.Fatal(err)
	}
	schema := string(schemaBytes)
	for _, want := range []string{
		"export type Post = typeof post.$inferSelect & {",
		"headline: string;",
		"export function computePost",
		`Object.defineProperty(result, "headline", { enumerable: true, get: () => result.title + "!" });`,
		"export function computeAuthor",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q:\n%s", want, schema)
		}
	}
	if strings.Contains(schema, "headline: text('headline')") {
		t.Fatal("computed headline was emitted as a persisted column")
	}
	apiTypes, err := os.ReadFile(filepath.Join(dir, "src/types/api.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiTypes), "headline: string;") || !strings.Contains(string(apiTypes), "displayName: string;") {
		t.Fatalf("frontend API types omit computed fields:\n%s", apiTypes)
	}
	if !strings.Contains(string(apiTypes), "posts: Array<Post & { author: Author | null }>") {
		t.Fatalf("frontend API response omits eager relationship:\n%s", apiTypes)
	}
	frontendSchemas, err := os.ReadFile(filepath.Join(dir, "src/types/schemas.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frontendSchemas), "headline: z.string()") || !strings.Contains(string(frontendSchemas), "displayName: z.string()") {
		t.Fatalf("frontend schemas omit computed fields:\n%s", frontendSchemas)
	}
	if !strings.Contains(string(frontendSchemas), "PostSchema.extend({ author: z.lazy(() => AuthorSchema).nullable() })") {
		t.Fatalf("frontend response schema omits eager relationship:\n%s", frontendSchemas)
	}

	routeBytes, err := os.ReadFile(filepath.Join(dir, "src/routes/posts.ts"))
	if err != nil {
		t.Fatal(err)
	}
	route := string(routeBytes)
	for _, want := range []string{
		"db.select({ _base: schema.post, author: schema.author }).from(schema.post)",
		".leftJoin(schema.author, eq(schema.post.authorId, schema.author.id))",
		"schema.computePost(_row._base as typeof schema.post.$inferSelect)",
		"schema.computeAuthor(_row.author as typeof schema.author.$inferSelect)",
		"headline: r.headline",
		"author: r.author == null ? null",
		"display_name: r.author.displayName",
		"schema.computePost((await db.insert(schema.post)",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("route missing %q:\n%s", want, route)
		}
	}
	authorRoute, err := os.ReadFile(filepath.Join(dir, "src/routes/authors.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(authorRoute), "schema.computeAuthor((await db.select().from(schema.author)") {
		t.Fatalf("fetch does not materialize author computed fields:\n%s", authorRoute)
	}
}

func TestGeneratedRelationshipsComputedTypeScriptCompiles(t *testing.T) {
	requireTypeScriptCompile(t, generateRelationshipsComputed(t))
}

func TestImplicitFKTraversalMaterializesTargetComputedFields(t *testing.T) {
	source := `blueprint "implicit-relationship" {
  version "1.0"
  runtime node
  database postgres
}
model author {
  id uuid primary
  name string required
  computed display_name string = name + "!"
}
model post {
  id uuid primary
  author_id uuid required ref(author)
}
GET /api/posts/:id {
  <- id uuid required
  |> post = fetch post(id)
  -> 200 { display_name: post.author.display_name }
}`
	file, parseErrors := parser.ParseFile("implicit-relationship.bp", []byte(source))
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %v", parseErrors)
	}
	if errors := checker.Check(file); len(errors) > 0 {
		t.Fatalf("check errors: %v", errors)
	}
	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("generate files: %v", err)
	}
	var routes strings.Builder
	for _, file := range files {
		if strings.HasPrefix(file.Path, "src/routes/") {
			routes.Write(file.Content)
		}
	}
	route := routes.String()
	for _, want := range []string{
		"const _author = schema.computeAuthor((await db.select().from(schema.author).where(eq(schema.author.id, post.authorId)))[0] as typeof schema.author.$inferSelect);",
		"display_name: _author.displayName",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("implicit FK route missing %q:\n%s", want, route)
		}
	}
}

func TestNodeRejectsWithCombinedWithLegacyQueryArguments(t *testing.T) {
	file, parseErrors := parser.ParseFile("legacy.bp", []byte(`blueprint "legacy" { version "1.0" runtime node database postgres }
model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> rows = query post { active: true } with(author) -> 200 { rows: rows } }`))
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %v", parseErrors)
	}
	if _, err := New().Files(file); err == nil || !strings.Contains(err.Error(), "legacy positional or block arguments") {
		t.Fatalf("block-argument error=%v", err)
	}

	file, parseErrors = parser.ParseFile("legacy.bp", []byte(`blueprint "legacy" { version "1.0" runtime node database postgres }
model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> rows = query post with(author) -> 200 { rows: rows } }`))
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %v", parseErrors)
	}
	ep := file.Blocks[2].(*ast.Endpoint)
	query := ep.Stmts[0].(*ast.StepStmt).Expr.(*ast.FnCall)
	query.Args = append(query.Args, &ast.IntLit{Value: "1"}, &ast.IntLit{Value: "20"})
	if _, err := New().Files(file); err == nil || !strings.Contains(err.Error(), "legacy positional or block arguments") {
		t.Fatalf("positional-argument error=%v", err)
	}
}
