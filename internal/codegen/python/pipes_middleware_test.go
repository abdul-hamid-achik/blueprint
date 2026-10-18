package python_test

import (
	"strings"
	"testing"
)

func TestPython_MiddlewareCallsPipeImportsFromPipesPackage(t *testing.T) {
	src := pipeHeader + `pipe check_token {
  <- token string
  |> guard token != "" -> 401 "Missing auth"
  -> token
}

middleware require_auth {
  before {
    |> checked = pipe check_token(header.Authorization)
    |> inject checked as auth
  }
}

GET /api/me {
  -> 200 "ok"
}
`
	outDir := buildPython(t, src)
	mw := readPy(t, outDir, "src/middleware/require_auth.py")
	if !strings.Contains(mw, "from src.pipes.check_token import check_token") {
		t.Errorf("expected pipe import in middleware, got:\n%s", mw)
	}
	if strings.Contains(mw, "from src.functions.check_token") {
		t.Errorf("pipe must not be imported from src.functions, got:\n%s", mw)
	}
}
