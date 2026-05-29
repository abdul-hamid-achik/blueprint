package python

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// genFunctions emits the wrapper-and-scaffold pair for every declared `fn`:
//
//   - src/functions/<fn_name>.py     — generated wrapper that re-exports the
//     user-owned implementation under the Blueprint name (snake_case),
//     so step calls in endpoint bodies can `from src.functions.<name>
//     import <name>` uniformly regardless of the impl module path.
//
//   - src/impl/functions/<module>.py — user-owned scaffold containing a
//     `raise NotImplementedError(...)` stub. Multiple fns sharing a module
//     are merged into one file. Marked UserOwned so subsequent builds never
//     overwrite a user-filled implementation.
//
// `impl node { module: "./internal/auth", func: "hashPassword" }` works as-is:
// the module path is reinterpreted as a Python package path
// (src/impl/functions/internal/auth.py) and the user-provided func name is
// preserved so the wrapper just calls it. Phase 3d does NOT require users
// to add `impl python { ... }` blocks — the existing `impl node` is enough
// to declare the contract; the user fills the Python side in `src/impl/`.
func (g *Generator) genFunctions(fns []*ast.Fn) []codegen.OutputFile {
	if len(fns) == 0 {
		return nil
	}

	refs := make([]implRef0, 0, len(fns))
	for _, fn := range fns {
		ref := implRef0{fnName: fn.Name, fn: fn}
		for _, inp := range fn.Inputs {
			ref.paramNames = append(ref.paramNames, inp.Name)
		}
		// Default location if no impl block: src/impl/functions/<fn_name>.py
		ref.moduleRel = fn.Name
		ref.funcName = fn.Name
		if fn.Impl != nil {
			for _, kv := range fn.Impl.Entries {
				switch kv.Key {
				case "module":
					if s, ok := kv.Value.(*ast.StringLit); ok {
						ref.moduleRel = pyImplModulePath(s.Value)
					}
				case "func":
					if s, ok := kv.Value.(*ast.StringLit); ok {
						ref.funcName = s.Value
					}
				}
			}
		}
		refs = append(refs, ref)
	}

	var out []codegen.OutputFile
	out = append(out, emptyInit("src/functions/__init__.py", g.sourceFile))
	out = append(out, emptyInit("src/impl/__init__.py", g.sourceFile))
	out = append(out, emptyInit("src/impl/functions/__init__.py", g.sourceFile))

	// One wrapper per fn.
	for _, ref := range refs {
		out = append(out, g.genFunctionWrapper(ref.fnName, ref.moduleRel, ref.funcName, ref.paramNames))
	}

	// Merge fns by impl module into a single scaffold file each.
	byModule := map[string][]implRef0{}
	for _, ref := range refs {
		byModule[ref.moduleRel] = append(byModule[ref.moduleRel], ref)
	}
	moduleNames := make([]string, 0, len(byModule))
	for m := range byModule {
		moduleNames = append(moduleNames, m)
	}
	sort.Strings(moduleNames)
	for _, m := range moduleNames {
		out = append(out, g.genFunctionScaffold(m, byModule[m]))
	}
	// Mirror the parent __init__.py markers for every subpackage so Python's
	// import machinery resolves the impl modules.
	dirs := map[string]bool{}
	for _, m := range moduleNames {
		parts := strings.Split(m, ".")
		for i := 1; i < len(parts); i++ {
			dirs[strings.Join(parts[:i], "/")] = true
		}
	}
	dirSlice := make([]string, 0, len(dirs))
	for d := range dirs {
		dirSlice = append(dirSlice, d)
	}
	sort.Strings(dirSlice)
	for _, d := range dirSlice {
		out = append(out, emptyInit("src/impl/functions/"+d+"/__init__.py", g.sourceFile))
	}

	return out
}

// genFunctionWrapper emits src/functions/<name>.py. The wrapper exposes the
// fn under its Blueprint name (snake_case), so endpoint bodies can call
// `hash_password(password)` regardless of what the impl module named the
// function. Step-call dispatch in endpoint_body.go relies on this convention.
func (g *Generator) genFunctionWrapper(fnName, moduleRel, funcName string, paramNames []string) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	fmt.Fprintf(&b, "from src.impl.functions.%s import %s as _impl\n\n", moduleRel, funcName)
	params := strings.Join(paramNames, ", ")
	fmt.Fprintf(&b, "def %s(%s):\n", fnName, params)
	if params == "" {
		b.WriteString("    return _impl()\n")
	} else {
		fmt.Fprintf(&b, "    return _impl(%s)\n", params)
	}
	return codegen.OutputFile{Path: "src/functions/" + fnName + ".py", Content: []byte(b.String())}
}

// genFunctionScaffold emits the user-owned impl file. Marked UserOwned so a
// later `bp build` never clobbers a real implementation. Multiple fns share
// a single scaffold when they share an impl module path.
func (g *Generator) genFunctionScaffold(moduleRel string, refs []implRef0) codegen.OutputFile {
	var b strings.Builder
	b.WriteString("# Blueprint implementation scaffold. This file is user-owned;\n")
	b.WriteString("# `bp build` will not overwrite it. Fill in the function bodies.\n\n")
	// Dedupe by funcName — two fns can map to the same impl name (rare but
	// possible) and a single `def` is enough.
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.funcName] {
			continue
		}
		seen[r.funcName] = true
		params := strings.Join(r.paramNames, ", ")
		fmt.Fprintf(&b, "def %s(%s):\n", r.funcName, params)
		fmt.Fprintf(&b, "    raise NotImplementedError(%q)\n\n", "Not implemented: "+r.funcName)
	}
	return codegen.OutputFile{
		Path:      "src/impl/functions/" + strings.ReplaceAll(moduleRel, ".", "/") + ".py",
		Content:   []byte(b.String()),
		UserOwned: true,
	}
}

// implRef0 is exported in this file's scope so genFunctionScaffold can take it
// without elevating to a package-level type that's used only here.
type implRef0 struct {
	fnName     string
	moduleRel  string
	funcName   string
	paramNames []string
	fn         *ast.Fn
}

// pyImplModulePath turns an impl module path like "./internal/auth" or
// "./internal/auth.js" into a Python dotted module path under
// src/impl/functions/, e.g. "internal.auth".
func pyImplModulePath(raw string) string {
	raw = strings.TrimPrefix(raw, "./")
	raw = strings.TrimPrefix(raw, "/")
	if dot := strings.LastIndex(raw, "."); dot > 0 && strings.HasPrefix(raw[dot:], ".") {
		ext := raw[dot:]
		if ext == ".js" || ext == ".ts" || ext == ".py" || ext == ".mjs" || ext == ".cjs" {
			raw = raw[:dot]
		}
	}
	parts := strings.Split(raw, "/")
	for i, p := range parts {
		parts[i] = path.Base(p)
	}
	return strings.Join(parts, ".")
}
