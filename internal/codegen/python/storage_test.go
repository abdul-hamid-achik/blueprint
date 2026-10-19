package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const storageS3Src = `blueprint "x" { version "1.0" port 3000 runtime node database postgres storage s3 }
secret DATABASE_URL required
secret S3_BUCKET required
model stored_file { id uuid primary  url string required }
POST /api/stored-files {
  <- body_text string required
  |> stored = upload(body_text, env.S3_BUCKET)
  |> record = save stored_file { url: stored.url }
  -> 201 { id: record.id, url: record.url }
}
GET /api/stored-files/:id {
  <- id uuid required
  |> record = fetch stored_file(id)
  |> guard record -> 404 "Stored file not found"
  |> stored_text = download(record.url)
  -> 200 { url: record.url, text: stored_text }
}
`

// TestPython_StorageS3EmitsBoto3Module asserts storage s3 produces the boto3
// module with the AWS settings the Node target reads, and that the handlers
// call it from sync defs so the blocking S3 calls run in FastAPI's threadpool.
func TestPython_StorageS3EmitsBoto3Module(t *testing.T) {
	outDir := buildPython(t, storageS3Src)

	storage := readPy(t, outDir, "src/lib/storage.py")
	for _, want := range []string{
		"import boto3",
		`s3 = boto3.client(`,
		"region_name=env.AWS_REGION",
		"endpoint_url=env.AWS_ENDPOINT_URL",
		"aws_access_key_id=env.AWS_ACCESS_KEY_ID",
		"aws_secret_access_key=env.AWS_SECRET_ACCESS_KEY",
		"aws_session_token=env.AWS_SESSION_TOKEN",
		"object_key = key if key is not None else str(uuid.uuid4())",
		`s3.put_object(Bucket=bucket, Key=object_key, Body=data, ContentType="application/octet-stream")`,
		`return StoredObject(url=f"s3://{bucket}/{object_key}", key=object_key)`,
		"parts = urlsplit(url)",
		`if parts.scheme != "s3" or not parts.netloc or not key:`,
		`s3.get_object(Bucket=parts.netloc, Key=key)`,
	} {
		if !strings.Contains(storage, want) {
			t.Errorf("storage.py missing %q, got:\n%s", want, storage)
		}
	}

	route := readPy(t, outDir, "src/routes/stored_files.py")
	for _, want := range []string{
		"from src.lib import storage",
		"def post_stored_files(",
		"stored = storage.upload(body_text, env.S3_BUCKET)",
		"def get_stored_files_id(",
		"stored_text = storage.download(record.url)",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("stored_files.py missing %q, got:\n%s", want, route)
		}
	}
	if strings.Contains(route, "async def") {
		t.Errorf("handlers calling storage must be sync def, got:\n%s", route)
	}

	pyproject := readPy(t, outDir, "pyproject.toml")
	if !strings.Contains(pyproject, `"boto3>=1.35"`) {
		t.Errorf("pyproject should depend on boto3, got:\n%s", pyproject)
	}

	env := readPy(t, outDir, "src/lib/env.py")
	for _, want := range []string{
		"AWS_REGION: Optional[str] = None",
		"AWS_ENDPOINT_URL: Optional[str] = None",
		"AWS_ACCESS_KEY_ID: Optional[str] = None",
		"AWS_SECRET_ACCESS_KEY: Optional[str] = None",
		"AWS_SESSION_TOKEN: Optional[str] = None",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env.py missing %q, got:\n%s", want, env)
		}
	}
}

// TestPython_NoStorageOmitsBoto3 asserts specs without a storage entry get no
// boto3 dependency, no storage module and no AWS settings.
func TestPython_NoStorageOmitsBoto3(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x { -> 200 "ok" }
`)
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/storage.py")); err == nil {
		t.Error("src/lib/storage.py should not be emitted without storage")
	}
	if strings.Contains(readPy(t, outDir, "pyproject.toml"), "boto3") {
		t.Error("pyproject should not depend on boto3 without storage")
	}
	if strings.Contains(readPy(t, outDir, "src/lib/env.py"), "AWS_") {
		t.Error("env.py should not declare AWS settings without storage")
	}
}

// TestPython_StorageRejections asserts the storage shapes Python still does not
// emit fail closed with a named reason.
func TestPython_StorageRejections(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"gcs backend": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node storage gcs }
GET /api/x { -> 200 "ok" }
`,
			want: `storage backend "gcs"`,
		},
		"local backend": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node storage local }
GET /api/x { -> 200 "ok" }
`,
			want: `storage backend "local"`,
		},
		"upload with one argument": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node storage s3 }
POST /api/x {
  <- body_text string required
  |> stored = upload(body_text)
  -> 201 { url: stored.url }
}
`,
			want: "`upload` with 1 arguments (expected 2)",
		},
		"download with two arguments": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node storage s3 }
GET /api/x {
  <- url string required
  |> body = download(url, url)
  -> 200 { url: url }
}
`,
			want: "`download` with 2 arguments (expected 1)",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q, got: %v", tc.want, err)
			}
		})
	}
}
