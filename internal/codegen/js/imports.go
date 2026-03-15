package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// --- Import Collection ---

// importCollector tracks what imports are needed for a generated file.
type importCollector struct {
	needsEnv        bool
	needsAnalytics  bool
	fnCalls         map[string]bool // user-declared fn names called
	pipeCalls       map[string]bool // user-declared pipe names called
	storageOps      map[string]bool // storage operations (upload, download)
	modelTypes      map[string]bool // model type names for type imports
	enumTypes       map[string]bool // enum type names for value imports
	structEnumTypes map[string]bool // struct enum names that also need <Name>Config import
	unknownCalls    map[string]bool // unrecognized calls (emit stubs)
	externalCalls   map[string]bool // external service call function names (call<Service>)
	saveHelpers     map[string]bool // generated save migration helpers
	stateHelpers    map[string]bool // generated state transition helpers
}

func newImportCollector() *importCollector {
	return &importCollector{
		fnCalls:         make(map[string]bool),
		pipeCalls:       make(map[string]bool),
		storageOps:      make(map[string]bool),
		modelTypes:      make(map[string]bool),
		enumTypes:       make(map[string]bool),
		structEnumTypes: make(map[string]bool),
		unknownCalls:    make(map[string]bool),
		externalCalls:   make(map[string]bool),
		saveHelpers:     make(map[string]bool),
		stateHelpers:    make(map[string]bool),
	}
}

// merge combines another collector's references into this one.
func (ic *importCollector) merge(other *importCollector) {
	if other.needsEnv {
		ic.needsEnv = true
	}
	if other.needsAnalytics {
		ic.needsAnalytics = true
	}
	for k := range other.fnCalls {
		ic.fnCalls[k] = true
	}
	for k := range other.pipeCalls {
		ic.pipeCalls[k] = true
	}
	for k := range other.storageOps {
		ic.storageOps[k] = true
	}
	for k := range other.modelTypes {
		ic.modelTypes[k] = true
	}
	for k := range other.enumTypes {
		ic.enumTypes[k] = true
	}
	for k := range other.structEnumTypes {
		ic.structEnumTypes[k] = true
	}
	for k := range other.unknownCalls {
		ic.unknownCalls[k] = true
	}
	for k := range other.externalCalls {
		ic.externalCalls[k] = true
	}
	for k := range other.saveHelpers {
		ic.saveHelpers[k] = true
	}
	for k := range other.stateHelpers {
		ic.stateHelpers[k] = true
	}
}

// collectImports scans arrow statements for import needs.
func (g *Generator) collectImports(stmts []ast.ArrowStmt) *importCollector {
	ic := newImportCollector()
	walkStmts(stmts, func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.FieldAccess:
			// env.X references
			if ident, ok := v.Base.(*ast.Ident); ok {
				if ident.Name == "env" {
					ic.needsEnv = true
				}
				// Enum.variant references (e.g., Plan.free)
				if g.declaredEnums[ident.Name] {
					ic.enumTypes[ident.Name] = true
				}
			}
		case *ast.FnCall:
			if v.Name == "track" {
				ic.needsAnalytics = true
				return
			}
			if v.Name == "transition" && len(v.Args) >= 1 {
				if ident, ok := v.Args[0].(*ast.Ident); ok {
					ic.stateHelpers[ident.Name] = true
				}
				return
			}
			if v.Name == "upgrade_save" && len(v.Args) >= 1 {
				if ident, ok := v.Args[0].(*ast.Ident); ok && g.declaredSaves[ident.Name] {
					ic.saveHelpers[ident.Name] = true
				}
				return
			}
			if isDataOp(v.Name) || isBuiltinFn(v.Name) {
				return
			}
			// Skip model names — they appear as FnCall nodes inside data op
			// patterns like fetch room(id) or as arguments to builtins like
			// join(room(id)), but they are not real function calls.
			if g.declaredModels[v.Name] {
				return
			}
			if v.Name == "upload" || v.Name == "download" {
				ic.storageOps[v.Name] = true
				return
			}
			// call <service> METHOD /path — track the service for external import
			if v.Name == "call" && len(v.Args) >= 1 {
				if svc, ok := v.Args[0].(*ast.Ident); ok {
					normalized := normalizeServiceName(svc.Name)
					if g.declaredExternals[normalized] {
						ic.externalCalls[normalized] = true
					}
				}
				return
			}
			if g.declaredPipes[v.Name] {
				ic.pipeCalls[v.Name] = true
			} else if g.declaredFns[v.Name] {
				ic.fnCalls[v.Name] = true
			} else {
				ic.unknownCalls[v.Name] = true
			}
		case *ast.IndexAccess:
			// Enum[key] references (e.g., Plan[key.plan])
			if ident, ok := v.Base.(*ast.Ident); ok {
				if g.declaredEnums[ident.Name] {
					ic.enumTypes[ident.Name] = true
					if g.structEnums[ident.Name] {
						ic.structEnumTypes[ident.Name] = true
					}
				}
			}
		}
	})
	return ic
}

// writeImports writes import statements based on collected references.
func (ic *importCollector) writeImports(b *strings.Builder, hasStorage bool) {
	if ic.needsEnv {
		b.WriteString("import { env } from '../lib/env.js';\n")
	}
	if ic.needsAnalytics {
		b.WriteString("import { track } from '../lib/analytics.js';\n")
	}
	if len(ic.saveHelpers) > 0 {
		names := sortedKeys2(ic.saveHelpers)
		helpers := make([]string, len(names))
		for i, name := range names {
			helpers[i] = "upgrade" + toPascalCase(name) + "Save"
		}
		fmt.Fprintf(b, "import { %s } from '../lib/save-migrations.js';\n", strings.Join(helpers, ", "))
	}
	if len(ic.stateHelpers) > 0 {
		names := sortedKeys2(ic.stateHelpers)
		helpers := make([]string, len(names))
		for i, name := range names {
			helpers[i] = "transition" + toPascalCase(name)
		}
		fmt.Fprintf(b, "import { %s } from '../lib/state.js';\n", strings.Join(helpers, ", "))
	}
	for _, name := range sortedKeys2(ic.fnCalls) {
		fmt.Fprintf(b, "import { %s } from '../functions/%s.js';\n",
			toCamelCase(name), toKebabCase(name))
	}
	for _, name := range sortedKeys2(ic.pipeCalls) {
		fmt.Fprintf(b, "import { %s } from '../pipes/%s.js';\n",
			toCamelCase(name), toKebabCase(name))
	}
	if len(ic.storageOps) > 0 {
		if hasStorage {
			ops := sortedKeys2(ic.storageOps)
			fmt.Fprintf(b, "import { %s } from '../lib/storage.js';\n",
				strings.Join(ops, ", "))
		} else {
			for k := range ic.storageOps {
				ic.unknownCalls[k] = true
			}
		}
	}
	if len(ic.modelTypes) > 0 {
		names := sortedKeys2(ic.modelTypes)
		tsNames := make([]string, len(names))
		for i, n := range names {
			tsNames[i] = toPascalCase(n)
		}
		fmt.Fprintf(b, "import type { %s } from '../models/schema.js';\n",
			strings.Join(tsNames, ", "))
	}
	if len(ic.enumTypes) > 0 {
		names := sortedKeys2(ic.enumTypes)
		// Include <Name>Config for struct enums that use bracket access
		var exports []string
		for _, n := range names {
			exports = append(exports, n)
			if ic.structEnumTypes[n] {
				exports = append(exports, n+"Config")
			}
		}
		fmt.Fprintf(b, "import { %s } from '../types.js';\n",
			strings.Join(exports, ", "))
	}
	// Emit imports for external service call functions
	if len(ic.externalCalls) > 0 {
		names := sortedKeys2(ic.externalCalls)
		callFns := make([]string, len(names))
		for i, n := range names {
			callFns[i] = "call" + toPascalCase(n)
		}
		fmt.Fprintf(b, "import { %s } from '../lib/external.js';\n",
			strings.Join(callFns, ", "))
	}
	// Emit stubs for unrecognized function calls
	for _, name := range sortedKeys2(ic.unknownCalls) {
		jsName := toCamelCase(name)
		fmt.Fprintf(b, "\n// TODO: implement %s\nasync function %s(...args: any[]): Promise<any> { return undefined; }\n",
			name, jsName)
	}
}

// writeDataImports adds standard data-access imports (db, schema, drizzle-orm operators)
// to a file builder. Call this for functions, schedules, workers, pipes, and middleware
// that contain data operations.
func writeDataImports(b *strings.Builder) {
	b.WriteString("import { db } from '../lib/db.js';\n")
	b.WriteString("import * as schema from '../models/schema.js';\n")
	b.WriteString("import { eq, ne, lt, gt, lte, gte, and, or, sql, desc, asc, inArray } from 'drizzle-orm';\n")
}

// stmtsHaveCall checks if any arrow statement contains a function call with the given name.
func stmtsHaveCall(stmts []ast.ArrowStmt, name string) bool {
	for _, s := range stmts {
		if step, ok := s.(*ast.StepStmt); ok {
			if fn, ok := step.Expr.(*ast.FnCall); ok && fn.Name == name {
				return true
			}
		}
		if when, ok := s.(*ast.WhenStmt); ok {
			if stmtsHaveCall(when.Body, name) {
				return true
			}
		}
	}
	return false
}

// stmtsHaveDataOps walks a list of arrow statements and returns true if any
// contain data operation calls (query, save, fetch, update, delete, etc.).
func stmtsHaveDataOps(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		if hasDataOpsInStmt(s) {
			return true
		}
	}
	return false
}

func hasDataOpsInStmt(s ast.ArrowStmt) bool {
	switch v := s.(type) {
	case *ast.StepStmt:
		return exprHasDataOp(v.Expr)
	case *ast.OutputStmt:
		if v.Value != nil {
			return exprHasDataOp(v.Value)
		}
	case *ast.GuardStmt:
		return exprHasDataOp(v.Condition)
	case *ast.WhenStmt:
		if v.Inline != nil && exprHasDataOp(v.Inline) {
			return true
		}
		if stmtsHaveDataOps(v.Body) {
			return true
		}
	case *ast.TryRecover:
		if stmtsHaveDataOps(v.Try) {
			return true
		}
		if stmtsHaveDataOps(v.Recover) {
			return true
		}
	}
	return false
}

func exprHasDataOp(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.FnCall:
		if isDataOp(v.Name) || isContentWorkflowFn(v.Name) {
			return true
		}
		// Recurse into function arguments to find nested data ops
		// e.g. map(items, save(...)) or pipe(query(...))
		for _, arg := range v.Args {
			if exprHasDataOp(arg) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return exprHasDataOp(v.Left) || exprHasDataOp(v.Right)
	case *ast.UnaryExpr:
		return exprHasDataOp(v.Operand)
	case *ast.ParenExpr:
		return exprHasDataOp(v.Expr)
	case *ast.BlockExpr:
		// Recurse into block expression entries for nested data ops
		for _, kv := range v.Entries {
			if exprHasDataOp(kv.Value) {
				return true
			}
		}
	}
	return false
}
