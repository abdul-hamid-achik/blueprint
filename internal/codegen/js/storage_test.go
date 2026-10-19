package js

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// download takes the s3:// URL that upload returns, is awaited, and returns
// the object's bytes; upload accepts a string body.
func TestStorageDownloadTakesURLAndIsAwaited(t *testing.T) {
	file, errs := parser.ParseFile("storage.bp", []byte(`blueprint "st" { version "1.0" runtime node database postgres storage s3 }
secret DATABASE_URL required
secret S3_BUCKET required
model stored_file {
  id  uuid   primary
  url string required
}
POST /api/files {
  <- body_text string required
  |> stored = upload(body_text, env.S3_BUCKET)
  |> record = save stored_file { url: stored.url }
  -> 201 { id: record.id }
}
GET /api/files/:id {
  <- id uuid required
  |> record = fetch stored_file(id)
  |> guard record -> 404 "Not found"
  |> bytes = download(record.url)
  -> 200 { size: bytes.length }
}
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	contents, paths := generatedFiles(t, file)
	routes := routeFileContent(t, paths, contents)
	if !strings.Contains(routes, "await download(record.url)") {
		t.Errorf("download must be awaited with the URL:\n%s", routes)
	}
	storage := contents["src/lib/storage.ts"]
	for _, want := range []string{
		"export async function upload(file: string | Uint8Array | Buffer | ReadableStream, bucket: string, key?: string)",
		"export async function download(urlOrBucket: string, key?: string): Promise<Buffer>",
		"return Buffer.from(await res.Body.transformToByteArray());",
	} {
		if !strings.Contains(storage, want) {
			t.Errorf("storage.ts missing %q:\n%s", want, storage)
		}
	}
}
