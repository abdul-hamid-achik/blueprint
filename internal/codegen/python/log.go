package python

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// pyLogMethods maps a Blueprint `log ... level(<name>)` level to the stdlib
// logger method that emits it. Mirrors the Node target's console.<name>
// mapping for debug/info/warn/error, with `warn` -> `warning` (stdlib name).
var pyLogMethods = map[string]string{
	"debug": "debug",
	"info":  "info",
	"warn":  "warning",
	"error": "error",
}

// genLogPy emits src/lib/log.py: a single module-level `logger` configured
// once on import with the stdlib `logging` module. Output is plain
// message-only text (like Node's console.* calls): debug/info go to stdout,
// warning/error go to stderr. Like src/lib/env.py it is always emitted, and
// endpoint/middleware files import it only when they contain a `log` step.
func (g *Generator) genLogPy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `import logging
import sys

logger = logging.getLogger("app")

if not logger.handlers:
    logger.setLevel(logging.DEBUG)
    logger.propagate = False
    formatter = logging.Formatter("%(message)s")

    stdout_handler = logging.StreamHandler(sys.stdout)
    stdout_handler.setLevel(logging.DEBUG)
    stdout_handler.addFilter(lambda record: record.levelno < logging.WARNING)
    stdout_handler.setFormatter(formatter)

    stderr_handler = logging.StreamHandler(sys.stderr)
    stderr_handler.setLevel(logging.WARNING)
    stderr_handler.setFormatter(formatter)

    logger.addHandler(stdout_handler)
    logger.addHandler(stderr_handler)
`
	return codegen.OutputFile{Path: "src/lib/log.py", Content: []byte(content)}
}

// pyLogLevelFeature returns an unsupported-feature reason when a `log` step's
// level(...) argument is not one of debug|info|warn|error. Node emits
// console.<name>(...) without checking, so an unknown level becomes a runtime
// TypeError there; Python fails closed at codegen instead. A `log` without a
// level defaults to info (matching Node's console.log).
func pyLogLevelFeature(fn *ast.FnCall) string {
	if len(fn.Args) < 2 {
		return ""
	}
	lvl, ok := fn.Args[1].(*ast.Ident)
	if !ok {
		return "`log` levels that are not identifiers"
	}
	if _, known := pyLogMethods[lvl.Name]; known {
		return ""
	}
	return fmt.Sprintf("`log` level %q (expected debug, info, warn, or error)", lvl.Name)
}
