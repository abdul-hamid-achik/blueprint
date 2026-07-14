package js

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func parseExternalSource(t *testing.T, src string) *ast.File {
	t.Helper()
	file, errs := parser.ParseFile("external-test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

func externalOutput(t *testing.T, src string) string {
	t.Helper()
	files, err := New().Files(parseExternalSource(t, src))
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	for _, file := range files {
		if file.Path == "src/lib/external.ts" {
			return string(file.Content)
		}
	}
	t.Fatal("src/lib/external.ts was not generated")
	return ""
}

func externalBlueprint(body string) string {
	return `blueprint "external-test" {
  version "0.1.0"
  port 3000
  runtime node
}

` + body
}

func TestExternalAuthAndRetryCodeShape(t *testing.T) {
	content := externalOutput(t, externalBlueprint(`secret SERVICE_TOKEN required

external "auth-service" {
  url: "http://auth:3001"
  timeout: 5s
  retry: 2
  auth: bearer(secret.SERVICE_TOKEN)
}`))

	for _, want := range []string{
		`auth: { strategy: "bearer", header: "Authorization", prefix: "Bearer ", credential: "SERVICE_TOKEN", value: env.SERVICE_TOKEN == null ? undefined : String(env.SERVICE_TOKEN) }`,
		`const retryCount = config.retry ?? 0;`,
		`for (let attempt = 0; attempt <= retryCount; attempt++)`,
		`if (attempt < retryCount) continue;`,
		`attempt < retryCount && retryableExternalStatus(res.status)`,
		`status === 408 || status === 429 || status >= 500`,
		`headers[config.auth.header] = config.auth.prefix + config.auth.value`,
		`return callExternal(authService, "auth-service", method, path, body)`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("external.ts missing %q\n%s", want, content)
		}
	}
	for _, invalid := range []string{"bearer(secret", "secret.SERVICE_TOKEN"} {
		if strings.Contains(content, invalid) {
			t.Errorf("external.ts must not emit raw credential expression %q\n%s", invalid, content)
		}
	}
}

func TestExternalAuthStrategies(t *testing.T) {
	tests := []struct {
		strategy string
		header   string
		prefix   string
	}{
		{strategy: "bearer", header: "Authorization", prefix: "Bearer "},
		{strategy: "jwt", header: "Authorization", prefix: "Bearer "},
		{strategy: "basic", header: "Authorization", prefix: "Basic "},
		{strategy: "api_key", header: "X-API-Key", prefix: ""},
	}

	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
			src := externalBlueprint(fmt.Sprintf(`secret TOKEN required

external "service" {
  url: "http://service"
  auth: %s(secret.TOKEN)
}`, tt.strategy))
			content := externalOutput(t, src)
			want := fmt.Sprintf(
				`auth: { strategy: %q, header: %q, prefix: %q, credential: "TOKEN", value: env.TOKEN == null ? undefined : String(env.TOKEN) }`,
				tt.strategy, tt.header, tt.prefix,
			)
			if !strings.Contains(content, want) {
				t.Fatalf("external.ts missing auth config %q\n%s", want, content)
			}
		})
	}
}

func TestExternalAuthMayUseDeclaredEnv(t *testing.T) {
	content := externalOutput(t, externalBlueprint(`env SERVICE_TOKEN "dev-token"

external "service" {
  url: "http://service"
  auth: bearer(env.SERVICE_TOKEN)
}`))
	if !strings.Contains(content, `value: env.SERVICE_TOKEN`) {
		t.Fatalf("external.ts should resolve declared env credential\n%s", content)
	}
}

func TestExternalConfigurationFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unsupported auth strategy",
			body: `secret TOKEN required
external "service" { auth: oauth(secret.TOKEN) }`,
			want: `unsupported auth strategy "oauth"`,
		},
		{
			name: "literal credential",
			body: `external "service" { auth: bearer("literal-token") }`,
			want: "must read its credential from secret.NAME or env.NAME",
		},
		{
			name: "undeclared secret",
			body: `external "service" { auth: bearer(secret.MISSING) }`,
			want: "secret.MISSING references an undeclared secret",
		},
		{
			name: "wrong auth arity",
			body: `secret TOKEN required
external "service" { auth: bearer(secret.TOKEN, secret.TOKEN) }`,
			want: "requires exactly one credential",
		},
		{
			name: "non integer retry",
			body: `external "service" { retry: "2" }`,
			want: "retry must be a non-negative integer",
		},
		{
			name: "duplicate retry",
			body: `external "service" {
  retry: 1
  retry: 2
}`,
			want: "duplicate retry entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := New().Files(parseExternalSource(t, externalBlueprint(tt.body)))
			if err == nil {
				t.Fatalf("expected generation to fail, got %d files", len(files))
			}
			if len(files) != 0 {
				t.Fatalf("fail-closed validation returned %d files", len(files))
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestExternalAuthAndRetryWithMockedFetch(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not available")
	}

	content := externalOutput(t, externalBlueprint(`secret SERVICE_TOKEN required

external "auth-service" {
  url: "http://auth:3001"
  timeout: 5s
  retry: 2
  auth: bearer(secret.SERVICE_TOKEN)
}`))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "external.ts"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env.js"), []byte(`export const env = { SERVICE_TOKEN: 'test-token' };`), 0o600); err != nil {
		t.Fatal(err)
	}
	testSource := `import { expect, test } from 'bun:test';
import { callAuthService } from './external.ts';

test('auth headers and retry policy are applied', async () => {
  let attempts = 0;
  globalThis.fetch = (async (input, init) => {
    attempts++;
    expect(String(input)).toBe('http://auth:3001/api/users');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe('{"id":7}');
    expect(init?.headers).toEqual({
      'Content-Type': 'application/json',
      Authorization: 'Bearer test-token',
    });
    if (attempts === 1) throw new Error('network unavailable');
    if (attempts === 2) {
      return new Response('unavailable', { status: 503, statusText: 'Service Unavailable' });
    }
    return Response.json({ ok: true });
  }) as typeof fetch;

  await expect(callAuthService('POST', '/api/users', { id: 7 })).resolves.toEqual({ ok: true });
  expect(attempts).toBe(3);

  attempts = 0;
  globalThis.fetch = (async () => {
    attempts++;
    return new Response('bad request', { status: 400, statusText: 'Bad Request' });
  }) as typeof fetch;
  await expect(callAuthService('GET', '/bad')).rejects.toThrow('External call failed: 400 Bad Request');
  expect(attempts).toBe(1);
});
`
	if err := os.WriteFile(filepath.Join(dir, "external.test.ts"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bun", "test", "external.test.ts")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mocked-fetch test failed: %v\n%s", err, output)
	}
}

func TestExternalAuthAndRetryTypeScriptCompiles(t *testing.T) {
	src := externalBlueprint(`secret SERVICE_TOKEN required

external "auth-service" {
  url: "http://auth:3001"
  timeout: 5s
  retry: 2
  auth: bearer(secret.SERVICE_TOKEN)
}`)
	outDir := t.TempDir()
	if err := New().Generate(parseExternalSource(t, src), outDir); err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	requireTypeScriptCompile(t, outDir)
}
