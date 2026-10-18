package python_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// pipeHeader is the blueprint block shared by the pipe tests. Pipes need no
// database, so the default target stays on the Phase 1 layer.
const pipeHeader = `blueprint "x" { version "1.0" port 3000 runtime node }
`

func TestPython_PipeEmitsModuleAndEndpointDispatch(t *testing.T) {
	src := pipeHeader + `pipe validate_input {
  <- data string

  |> guard data != "" -> 400 "Data cannot be empty"

  -> data
}

fn transform {
  <- data string
  -> result string

  impl node {
    module: "./internal/transform"
  }
}

POST /api/process {
  <- data string required

  |> validated = pipe validate_input(data)
  |> result = transform(validated)

  -> 200 { result: result }
}
`
	outDir := buildPython(t, src)

	mod := readPy(t, outDir, "src/pipes/validate_input.py")
	for _, want := range []string{
		"from fastapi import HTTPException\n",
		"def validate_input(data):\n",
		`    if not ((data != "")):` + "\n" + `        raise HTTPException(status_code=400, detail="Data cannot be empty")`,
		"    return data\n",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("pipe module missing %q, got:\n%s", want, mod)
		}
	}
	if strings.Contains(mod, "TODO") || strings.Contains(mod, "NotImplementedError") {
		t.Errorf("pipe module must not contain placeholders, got:\n%s", mod)
	}
	readPy(t, outDir, "src/pipes/__init__.py")

	route := readPy(t, outDir, "src/routes/process.py")
	if !strings.Contains(route, "from src.pipes.validate_input import validate_input") {
		t.Errorf("expected pipe import in route file, got:\n%s", route)
	}
	if !strings.Contains(route, "validated = validate_input(data)") {
		t.Errorf("expected pipe step dispatch, got:\n%s", route)
	}
	if strings.Contains(route, "from src.functions.validate_input") {
		t.Errorf("pipe must not be imported from src.functions, got:\n%s", route)
	}
	if !strings.Contains(route, "from src.functions.transform import transform") {
		t.Errorf("fn import should be unchanged, got:\n%s", route)
	}
}

func TestPython_PipeWithoutInputUsesImplicitParam(t *testing.T) {
	src := pipeHeader + `pipe greeting {
  -> "hello"
}

GET /api/hi {
  |> msg = pipe greeting()
  -> 200 { msg: msg }
}
`
	outDir := buildPython(t, src)
	mod := readPy(t, outDir, "src/pipes/greeting.py")
	if !strings.Contains(mod, "def greeting(input):\n") {
		t.Errorf("expected implicit input parameter, got:\n%s", mod)
	}
	if !strings.Contains(mod, `    return "hello"`+"\n") {
		t.Errorf("expected string output as plain return, got:\n%s", mod)
	}
	if strings.Contains(mod, "JSONResponse") {
		t.Errorf("pipe outputs must not be HTTP responses, got:\n%s", mod)
	}
}

func TestPython_PipeCallsPipeAndFnWithBlockBinding(t *testing.T) {
	src := pipeHeader + `fn score {
  <- text string
  -> int

  impl node {
    module: "./internal/score"
  }
}

pipe normalize {
  <- text string
  |> cleaned = score(text)
  |> meta = { source: "api", kind: "text" }
  -> meta
}

pipe outer {
  <- text string
  |> inner = pipe normalize(text)
  -> inner
}

POST /api/x {
  <- text string required
  |> out = pipe outer(text)
  -> 200 { out: out }
}
`
	outDir := buildPython(t, src)
	mod := readPy(t, outDir, "src/pipes/normalize.py")
	for _, want := range []string{
		"from src.functions.score import score\n",
		"def normalize(text):\n",
		"    cleaned = score(text)\n",
		`    meta = {"source": "api", "kind": "text"}` + "\n",
		"    return meta\n",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("normalize module missing %q, got:\n%s", want, mod)
		}
	}
	outer := readPy(t, outDir, "src/pipes/outer.py")
	if !strings.Contains(outer, "from src.pipes.normalize import normalize\n") {
		t.Errorf("expected pipe-to-pipe import, got:\n%s", outer)
	}
	if !strings.Contains(outer, "    inner = normalize(text)\n") {
		t.Errorf("expected pipe-to-pipe dispatch, got:\n%s", outer)
	}
}

func TestPython_PipeRejectsUnsupportedBodiesFailClosed(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"data operation in pipe": {
			src:  "pipe p { <- v string  |> row = fetch item(v)  -> row }\n",
			want: `step "fetch" in pipe "p"`,
		},
		"log step in pipe": {
			src:  "pipe p { <- v string  |> log \"hi\"  -> v }\n",
			want: `step "log" in pipe "p"`,
		},
		"env read in guard": {
			src:  "pipe p { <- v string  |> guard v != env.KEY -> 400 \"no\"  -> v }\n",
			want: "`env.KEY` in pipe",
		},
		"when block": {
			src:  "pipe p { <- v string  |> when v == \"a\" { -> v } -> v }\n",
			want: "`when` blocks in pipe",
		},
		"status on output": {
			src:  "pipe p { <- v string  -> 201 v }\n",
			want: "status code on the output of pipe",
		},
		"call inside expression": {
			src:  "pipe p { <- v string  |> guard len(v) > 0 -> 400 \"no\"  -> v }\n",
			want: "inside an expression in pipe",
		},
		"mutually recursive pipes": {
			src:  "pipe a { <- v string  |> r = pipe b(v)  -> r }\npipe b { <- v string  |> r = pipe a(v)  -> r }\n",
			want: "recursive pipe call graph",
		},
		"self-recursive pipe": {
			src:  "pipe a { <- v string  |> r = pipe a(v)  -> r }\n",
			want: "recursive pipe call graph",
		},
		"python keyword pipe name": {
			src:  "pipe class { <- v string  -> v }\n",
			want: `pipe name "class"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := pipeHeader + tc.src + "GET /api/x { -> 200 \"ok\" }\n"
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected fail-closed error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got: %v", tc.want, err)
			}
		})
	}
}
