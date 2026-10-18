package python_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// TestPython_LogLevelsSelectLoggerMethod covers every supported level and the
// default. `warn` maps to the stdlib `warning` method; the message keeps the
// same f-string interpolation as the plain form.
func TestPython_LogLevelsSelectLoggerMethod(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  <- name string required
  |> log "d {name}" level(debug)
  |> log "i {name}" level(info)
  |> log "w {name}" level(warn)
  |> log "e {name}" level(error)
  |> log "plain {name}"
  -> 200 "ok"
}
`
	body := readPy(t, buildPython(t, src), "src/routes/x.py")
	for _, want := range []string{
		`    logger.debug(f"d {name}")`,
		`    logger.info(f"i {name}")`,
		`    logger.warning(f"w {name}")`,
		`    logger.error(f"e {name}")`,
		`    logger.info(f"plain {name}")`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "print(") {
		t.Errorf("log steps must not emit print(), got:\n%s", body)
	}
}

// TestPython_LogUnknownLevelFailsClosed: an unknown level is a codegen error
// (Node would emit console.<level>(...) and crash at runtime instead).
func TestPython_LogUnknownLevelFailsClosed(t *testing.T) {
	cases := map[string]string{
		"endpoint": `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  |> log "boom" level(bogus)
  -> 200 "ok"
}
`,
		"try recover": `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model order { id uuid primary  status string default("pending") }
POST /api/orders {
  |> try {
    |> order = save order { status: "pending" }
  } recover {
    |> log "failed" level(fatal)
    -> 500 "boom"
  }
  -> 201 "ok"
}
`,
		"middleware": `blueprint "x" { version "1.0" port 3000 runtime node }
middleware audit {
  before {
    |> log "audit" level(trace)
  }
}
GET /api/x { use audit  -> 200 "ok" }
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected an unsupported-level error, got none")
			}
			if !strings.Contains(err.Error(), "`log` level") || !strings.Contains(err.Error(), "expected debug, info, warn, or error") {
				t.Errorf("error should name the bad level and the valid set, got: %v", err)
			}
		})
	}
}

// TestPython_LogModuleEmittedAndConfiguredOnce checks the shared logger module.
// It is always emitted (like src/lib/env.py) and configures its handlers once.
func TestPython_LogModuleEmittedAndConfiguredOnce(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x { -> 200 "ok" }
`
	logPy := readPy(t, buildPython(t, src), "src/lib/log.py")
	for _, want := range []string{
		"import logging",
		`logger = logging.getLogger("app")`,
		"if not logger.handlers:",
		"logger.propagate = False",
		`logging.Formatter("%(message)s")`,
		"logging.StreamHandler(sys.stdout)",
		"logging.StreamHandler(sys.stderr)",
		"logging.WARNING",
	} {
		if !strings.Contains(logPy, want) {
			t.Errorf("src/lib/log.py missing %q in:\n%s", want, logPy)
		}
	}
	if strings.Contains(logPy, "import structlog") || strings.Contains(logPy, "import json") {
		t.Errorf("log module must stay stdlib-only with plain-text output, got:\n%s", logPy)
	}
}

// TestPython_LogImportOnlyWhenUsed: endpoint files import the logger only when
// some step (including one nested in try/recover) actually logs.
func TestPython_LogImportOnlyWhenUsed(t *testing.T) {
	noLog := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x { -> 200 "ok" }
`)
	route := readPy(t, noLog, "src/routes/x.py")
	if strings.Contains(route, "src.lib.log") || strings.Contains(route, "logger") {
		t.Errorf("route without log must not import the logger, got:\n%s", route)
	}

	nested := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model order { id uuid primary  status string default("pending") }
POST /api/orders {
  |> try {
    |> order = save order { status: "pending" }
  } recover {
    |> log "failed: {error.message}" level(error)
    -> 500 "boom"
  }
  -> 201 "ok"
}
`)
	route = readPy(t, nested, "src/routes/orders.py")
	if !strings.Contains(route, "from src.lib.log import logger\n") {
		t.Errorf("log nested in recover must import the logger, got:\n%s", route)
	}
	if !strings.Contains(route, `logger.error(f"failed: {str(error)}")`) {
		t.Errorf("recover log should be logger.error with str(error), got:\n%s", route)
	}
}

// TestPython_LogMiddlewareImportsLogger: middleware files get the logger import
// only when a before-step logs.
func TestPython_LogMiddlewareImportsLogger(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
middleware audit {
  before {
    |> log "audit request" level(warn)
  }
}
middleware quiet {
  before {
    |> guard header.X-Key == "k" -> 401 "no"
  }
}
GET /api/x { use audit  use quiet  -> 200 "ok" }
`)
	audit := readPy(t, outDir, "src/middleware/audit.py")
	if !strings.Contains(audit, "from src.lib.log import logger\n") {
		t.Errorf("middleware with log must import the logger, got:\n%s", audit)
	}
	if !strings.Contains(audit, "logger.warning(") {
		t.Errorf("middleware warn should map to logger.warning, got:\n%s", audit)
	}
	quiet := readPy(t, outDir, "src/middleware/quiet.py")
	if strings.Contains(quiet, "src.lib.log") {
		t.Errorf("middleware without log must not import the logger, got:\n%s", quiet)
	}
}
