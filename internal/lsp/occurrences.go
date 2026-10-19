package lsp

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// symOccurrence is one source site of a symbol. Off and Len cover the name
// token in the document (byte offsets). Key identifies the symbol: "<kind>:<name>"
// for top-level declarations and "local:<declOffset>" for endpoint-local
// bindings. Decl marks the declaring site.
type symOccurrence struct {
	Off  int
	Len  int
	Name string
	Key  string
	Decl bool
}

// globalSym is a top-level declaration. Kind is one of model, enum, type, fn,
// pipe, middleware, external.
type globalSym struct {
	Kind string
}

// symbolIndex is the scope-aware occurrence index for one document. It is
// built from the AST and resolved against lexer tokens, so text inside
// strings, intents and comments can never match.
type symbolIndex struct {
	text    string
	toks    []lexer.Token
	byOff   map[int]int // token start offset -> token index
	globals map[string]globalSym
	// imported holds the top-level declarations of the other files in the
	// document's include component. They resolve references but are not this
	// file's declarations, so they never produce a declaration occurrence.
	imported map[string]globalSym
	// externals are keyed by normalizeExternalName, because `call` names are
	// identifiers while `external` declares a quoted, possibly hyphenated name.
	externals map[string]externalSym
	// localBlock and localName describe each endpoint-local binding by key.
	localBlock map[string]int
	localName  map[string]string
	// mapDerived holds model names used as `map ...: <data op> model` bodies.
	// The checker derives an implicit result binding from such a name, so
	// renaming the model would also change a binding the index does not own.
	mapDerived map[string]bool
	// unresolved holds every name read in value position that no visible
	// binding or enum declares. Such a name may be bound implicitly or injected
	// from outside the block (middleware aliases, WebSocket message, webhook
	// data), so a rename must not capture it even though no checker error says so.
	unresolved map[string]bool
	occs       []symOccurrence
	// mismatches counts AST sites whose location did not point at a token
	// spelled like the name. Such sites are dropped rather than guessed; the
	// rename guard in rename.go refuses edits that would leave a stale name.
	mismatches int
}

// externalSym is an `external "name"` declaration. Raw is the declared text and
// Off is the byte offset of its first character inside the quotes.
type externalSym struct {
	Raw string
	Off int
}

// scopeFrame is one statement list. Bindings declared in a list are visible in
// that list and in lists nested inside it (when/try bodies), in source order.
type scopeFrame struct {
	decls map[string]localDecl
}

type localDecl struct {
	off   int
	input bool
}

type occurrenceBuilder struct {
	idx    *symbolIndex
	block  int
	frames []*scopeFrame
	// inMiddleware is set while walking a middleware. An `inject ... as name`
	// there is shared by every endpoint that uses the middleware, and the
	// index only sees one block, so the alias is not treated as a binding.
	inMiddleware bool
	// mapBody counts the nesting of `map` bodies. Inside one, a model name is
	// an implicit alias for the element, as the checker binds it.
	mapBody int
	// deferred lists field types that global must not walk, because topLevel
	// walks them later inside the block's statement scope.
	deferred map[reflect.Type]bool
}

var (
	endpointMetaListType = reflect.TypeOf([]*ast.EndpointMeta(nil))
	testRequestType      = reflect.TypeOf((*ast.TestRequest)(nil))
	assertionListType    = reflect.TypeOf([]*ast.Assertion(nil))
)

// dataOpNames are the data operations whose first argument names a model or a
// bound value.
var dataOpNames = map[string]bool{
	"query": true, "fetch": true, "save": true, "update": true, "delete": true,
	"count": true, "seed": true, "import_bundle": true, "export_bundle": true,
}

var (
	arrowListType   = reflect.TypeOf([]ast.ArrowStmt(nil))
	logicBlockType  = reflect.TypeOf((*ast.LogicBlock)(nil))
	inputListType   = reflect.TypeOf([]*ast.InputStmt(nil))
	outputListType  = reflect.TypeOf([]*ast.OutputStmt(nil))
	handlerListType = reflect.TypeOf([]*ast.StreamHandler(nil))
	locType         = reflect.TypeOf(lexer.Loc{})
)

// skipInGlobalWalk reports whether a field type holds statement lists or
// scoped blocks. Those are walked explicitly, in order, by topLevel.
func skipInGlobalWalk(t reflect.Type) bool {
	switch t {
	case arrowListType, logicBlockType, inputListType, outputListType, handlerListType, locType:
		return true
	}
	return false
}

// buildSymbolIndex walks a parsed document and records every occurrence of a
// top-level declaration or endpoint-local binding.
func buildSymbolIndex(doc *docIndex) *symbolIndex {
	return buildSymbolIndexWith(doc, nil)
}

// buildSymbolIndexWith is buildSymbolIndex for a document whose include
// component declares more names: imported maps those names to their
// declarations in the other files. A nil map means no imports.
func buildSymbolIndexWith(doc *docIndex, imported map[string]globalSym) *symbolIndex {
	s := &symbolIndex{
		text:       "",
		byOff:      map[int]int{},
		globals:    map[string]globalSym{},
		imported:   imported,
		externals:  map[string]externalSym{},
		localBlock: map[string]int{},
		localName:  map[string]string{},
		mapDerived: map[string]bool{},
		unresolved: map[string]bool{},
	}
	if doc == nil || doc.file == nil {
		return s
	}
	s.text = doc.text
	s.toks = doc.file.SourceTokens
	if len(s.toks) == 0 {
		s.toks, _ = lexer.Tokenize("", []byte(doc.text))
	}
	for i, t := range s.toks {
		if _, dup := s.byOff[t.Loc.Offset]; !dup {
			s.byOff[t.Loc.Offset] = i
		}
	}

	b := &occurrenceBuilder{idx: s, block: -1}
	for _, block := range doc.file.Blocks {
		b.collectGlobal(block)
	}
	for i, block := range doc.file.Blocks {
		b.topLevel(i, block)
	}
	if doc.file.Blueprint != nil {
		b.block = -1
		b.frames = nil
		b.global(reflect.ValueOf(doc.file.Blueprint))
	}

	sort.SliceStable(s.occs, func(i, j int) bool { return s.occs[i].Off < s.occs[j].Off })
	dedup := s.occs[:0]
	for i, o := range s.occs {
		if i > 0 && o.Off == s.occs[i-1].Off {
			continue
		}
		dedup = append(dedup, o)
	}
	s.occs = dedup
	return s
}

// collectGlobal registers a top-level declaration and its name occurrence.
func (b *occurrenceBuilder) collectGlobal(block ast.TopLevel) {
	var kind, name string
	var loc lexer.Loc
	switch d := block.(type) {
	case *ast.Model:
		kind, name, loc = "model", d.Name, d.Loc
	case *ast.Content:
		kind, name, loc = "model", d.Name, d.Loc
	case *ast.Enum:
		kind, name, loc = "enum", d.Name, d.Loc
	case *ast.TypeDecl:
		kind, name, loc = "type", d.Name, d.Loc
	case *ast.Alias:
		kind, name, loc = "type", d.Name, d.Loc
	case *ast.Fn:
		kind, name, loc = "fn", d.Name, d.Loc
	case *ast.Pipe:
		kind, name, loc = "pipe", d.Name, d.Loc
	case *ast.Middleware:
		kind, name, loc = "middleware", d.Name, d.Loc
	case *ast.External:
		b.collectExternal(d)
		return
	default:
		return
	}
	if _, dup := b.idx.globals[name]; dup || name == "" {
		return
	}
	off, ok := b.nameAfter(loc, name)
	if !ok {
		return
	}
	b.idx.globals[name] = globalSym{Kind: kind}
	b.add(off, name, kind+":"+name, true)
}

// collectExternal registers `external "name"`. The declared name is a string
// token, so the occurrence covers the text between the quotes.
func (b *occurrenceBuilder) collectExternal(d *ast.External) {
	i, ok := b.idx.byOff[d.Loc.Offset]
	if !ok || i+1 >= len(b.idx.toks) || b.idx.toks[i+1].Kind != lexer.TokenString || d.Name == "" {
		b.idx.mismatches++
		return
	}
	key := normalizeExternal(d.Name)
	if _, dup := b.idx.externals[key]; dup {
		return
	}
	off := b.idx.toks[i+1].Loc.Offset + 1
	if !strings.HasPrefix(b.idx.text[off:], d.Name) {
		b.idx.mismatches++
		return
	}
	b.idx.externals[key] = externalSym{Raw: d.Name, Off: off}
	b.idx.occs = append(b.idx.occs, symOccurrence{Off: off, Len: len(d.Name), Name: d.Name, Key: "external:" + key, Decl: true})
}

// normalizeExternal mirrors checker.normalizeExternalName, which matches a
// `call` service identifier to its `external` declaration.
func normalizeExternal(name string) string {
	name = strings.ToLower(strings.Trim(name, `"`))
	var b strings.Builder
	prevUnderscore := false
	for _, r := range name {
		if r == '-' || r == ' ' || r == '.' {
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		prevUnderscore = r == '_'
	}
	return b.String()
}

// topLevel resolves every reference inside one top-level block. Statement
// lists get their own frames, mirroring the checker's per-list scopes.
func (b *occurrenceBuilder) topLevel(block int, node ast.TopLevel) {
	b.block = block
	b.frames = nil
	_, b.inMiddleware = node.(*ast.Middleware)
	b.deferred = nil
	switch node.(type) {
	case *ast.Endpoint:
		// Meta is checked against the endpoint's own statements.
		b.deferred = map[reflect.Type]bool{endpointMetaListType: true}
	case *ast.Test:
		// Test requests and assertions read the setup bindings.
		b.deferred = map[reflect.Type]bool{testRequestType: true, assertionListType: true}
	}
	b.global(reflect.ValueOf(node))
	b.deferred = nil

	switch n := node.(type) {
	case *ast.Endpoint:
		b.push()
		b.statements(n.Stmts)
		b.pathParams(n.Loc, n.Path)
		b.global(reflect.ValueOf(n.Meta))
		b.pop()
	case *ast.WsEndpoint:
		// on_message and on_disconnect are clones of the on_connect scope.
		b.push()
		b.statements(n.Stmts)
		b.pathParams(n.Loc, n.Path)
		b.statements(n.OnConnect)
		b.list(n.OnMessage)
		b.list(n.OnDisconnect)
		b.pop()
	case *ast.Fn:
		b.push()
		for _, in := range n.Inputs {
			b.input(in)
		}
		for _, out := range n.Outputs {
			b.expr(out.Value)
		}
		if n.Logic != nil {
			b.statements(n.Logic.Stmts)
		}
		b.pop()
	case *ast.Pipe:
		b.list(n.Stmts)
	case *ast.Middleware:
		b.push()
		b.statements(n.Before)
		b.statements(n.After)
		b.pop()
	case *ast.StreamEndpoint:
		// Handler conditions and bodies see the endpoint's statements.
		b.push()
		b.statements(n.Stmts)
		b.pathParams(n.Loc, n.Path)
		for _, h := range n.Handlers {
			b.expr(h.Condition)
			b.list(h.Body)
		}
		b.pop()
	case *ast.Worker:
		b.list(n.Stmts)
		b.list(n.OnFail)
	case *ast.Schedule:
		b.list(n.Stmts)
	case *ast.Test:
		b.push()
		b.statements(n.Setup)
		b.global(reflect.ValueOf(n.Request))
		b.global(reflect.ValueOf(n.Expect))
		b.statements(n.Cleanup)
		b.pop()
	case *ast.TestGroup:
		b.list(n.SharedSetup)
	case *ast.Subscribe:
		b.list(n.Stmts)
		b.subscribeFrom(n)
	case *ast.SaveSchema:
		b.saveSchemaModel(n)
	case *ast.Fixture:
		b.fixtureSeedModel(n)
	}
	b.frames = nil
}

// pathParams links each :param segment of an endpoint path to the <- input
// of the same name, so renaming the input also renames the segment. Without
// that, a REST input would silently stop reading the path parameter, and a
// STREAM or WS input would no longer name one (C022). Segments with no
// matching input stay untracked. loc is the method token; the path token
// follows it.
func (b *occurrenceBuilder) pathParams(loc lexer.Loc, path string) {
	if len(b.frames) == 0 {
		return
	}
	i, ok := b.idx.byOff[loc.Offset]
	if !ok || i+1 >= len(b.idx.toks) {
		return
	}
	tok := b.idx.toks[i+1]
	if tok.Kind != lexer.TokenPath || tok.Value != path {
		return
	}
	frame := b.frames[len(b.frames)-1]
	off := tok.Loc.Offset
	for _, segment := range strings.Split(path, "/") {
		if name, ok := strings.CutPrefix(segment, ":"); ok {
			if d, declared := frame.decls[name]; declared && d.input {
				b.addRaw(off+1, name, localKey(d.off), false)
			}
		}
		off += len(segment) + 1
	}
}

// input declares a <- input in the current frame.
func (b *occurrenceBuilder) input(in *ast.InputStmt) {
	if off, ok := b.nameAfter(in.Loc, in.Name); ok {
		b.declare(in.Name, off, true)
	}
	b.global(reflect.ValueOf(in.Type))
	for _, c := range in.Constraints {
		b.global(reflect.ValueOf(c))
	}
}

func (b *occurrenceBuilder) statements(stmts []ast.ArrowStmt) {
	for _, st := range stmts {
		switch s := st.(type) {
		case *ast.InputStmt:
			b.input(s)
		case *ast.StepStmt:
			b.expr(s.Expr)
			if s.Binding != "" {
				if off, ok := b.nameAfter(s.Loc, s.Binding); ok {
					b.bind(s.Binding, off)
				}
			}
		case *ast.GuardStmt:
			b.expr(s.Condition)
		case *ast.WhenStmt:
			b.expr(s.Condition)
			b.expr(s.Inline)
			b.list(s.Body)
		case *ast.OutputStmt:
			b.expr(s.Value)
		case *ast.TryRecover:
			// The checker merges successful try bindings into the enclosing
			// scope, so following steps may read them; recover bindings stay local.
			b.push()
			b.statements(s.Try)
			tryDecls := b.frames[len(b.frames)-1].decls
			b.pop()
			outer := b.frames[len(b.frames)-1].decls
			for name, d := range tryDecls {
				if _, exists := outer[name]; !exists {
					outer[name] = d
				}
			}
			b.list(s.Recover)
		case *ast.GenerateStep:
			for _, h := range s.Hints {
				b.expr(h.Value)
			}
		}
	}
}

// list resolves a nested statement list in its own frame.
func (b *occurrenceBuilder) list(stmts []ast.ArrowStmt) {
	b.push()
	b.statements(stmts)
	b.pop()
}

func (b *occurrenceBuilder) push() {
	b.frames = append(b.frames, &scopeFrame{decls: map[string]localDecl{}})
}

func (b *occurrenceBuilder) pop() {
	b.frames = b.frames[:len(b.frames)-1]
}

func (b *occurrenceBuilder) lookup(name string) (localDecl, bool) {
	for i := len(b.frames) - 1; i >= 0; i-- {
		if d, ok := b.frames[i].decls[name]; ok {
			return d, true
		}
	}
	return localDecl{}, false
}

// declare records a new binding in the innermost frame.
func (b *occurrenceBuilder) declare(name string, off int, input bool) {
	if len(b.frames) == 0 {
		return
	}
	b.frames[len(b.frames)-1].decls[name] = localDecl{off: off, input: input}
	key := localKey(off)
	b.idx.localBlock[key] = b.block
	b.idx.localName[key] = name
	b.add(off, name, key, true)
}

// bind handles `|> name = ...`. Rebinding an input, or a name already declared
// in the same list, assigns the existing symbol. Otherwise the step introduces
// a new binding, which shadows any outer one, as the checker does.
func (b *occurrenceBuilder) bind(name string, off int) {
	if d, ok := b.lookup(name); ok && (d.input || b.innermostHas(name)) {
		b.add(off, name, localKey(d.off), false)
		return
	}
	b.declare(name, off, false)
}

func (b *occurrenceBuilder) innermostHas(name string) bool {
	if len(b.frames) == 0 {
		return false
	}
	_, ok := b.frames[len(b.frames)-1].decls[name]
	return ok
}

func localKey(declOff int) string {
	return "local:" + strconv.Itoa(declOff)
}

// expr resolves value references. Model targets, columns, event names and
// other symbolic arguments are handled by the call that owns them.
func (b *occurrenceBuilder) expr(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Ident:
		b.ident(x)
	case *ast.FnCall:
		b.call(x)
	case *ast.BinaryExpr:
		b.expr(x.Left)
		b.expr(x.Right)
	case *ast.UnaryExpr:
		b.expr(x.Operand)
	case *ast.FieldAccess:
		b.expr(x.Base)
	case *ast.IndexAccess:
		b.expr(x.Base)
		b.expr(x.Index)
	case *ast.ParenExpr:
		b.expr(x.Expr)
	case *ast.ListExpr:
		for _, el := range x.Elements {
			b.expr(el)
		}
	case *ast.BlockExpr:
		for _, kv := range x.Entries {
			b.expr(kv.Value)
		}
	case *ast.StringLit:
		b.interpolations(x)
	}
}

func (b *occurrenceBuilder) ident(x *ast.Ident) {
	if strings.HasPrefix(x.Name, "pipe_") && b.tokenKind(x.Loc.Offset) == lexer.TokenPipe {
		// Bare `pipe name` is parsed as an identifier located at the keyword.
		name := strings.TrimPrefix(x.Name, "pipe_")
		if off, ok := b.nameAfter(x.Loc, name); ok && b.kindOf(name) == "pipe" {
			b.add(off, name, "pipe:"+name, false)
		}
		return
	}
	if x.Name == "close" && b.tokenKind(x.Loc.Offset) == lexer.TokenClose {
		return
	}
	b.resolveValue(x.Name, x.Loc.Offset, true)
}

// resolveValue records a value reference to name at off: an endpoint-local
// binding visible from here, or else a global enum. Tokens are checked by
// add; string interpolation offsets use addRaw.
func (b *occurrenceBuilder) resolveValue(name string, off int, token bool) {
	record := func(key string) {
		if token {
			b.add(off, name, key, false)
		} else {
			b.addRaw(off, name, key, false)
		}
	}
	if d, ok := b.lookup(name); ok {
		record(localKey(d.off))
		return
	}
	switch b.kindOf(name) {
	case "enum":
		record("enum:" + name)
		return
	case "model":
		if b.mapBody > 0 {
			record("model:" + name)
			return
		}
	}
	b.idx.unresolved[name] = true
}

// subscribeFrom resolves the external named in `subscribe "event" from(service)`.
func (b *occurrenceBuilder) subscribeFrom(n *ast.Subscribe) {
	i, ok := b.idx.byOff[n.Loc.Offset]
	if !ok || n.From == "" {
		return
	}
	key := normalizeExternal(n.From)
	if b.idx.externals[key].Raw == "" {
		return
	}
	for j := i; j+2 < len(b.idx.toks) && j < i+8; j++ {
		if b.idx.toks[j].Kind == lexer.TokenFrom {
			if name := b.idx.toks[j+2]; name.Value == n.From {
				b.add(name.Loc.Offset, n.From, "external:"+key, false)
			}
			return
		}
	}
}

// interpolations resolves `{name...}` roots inside a string literal, which the
// checker reads as bindings. Only the roots are recorded; the rest of the
// literal is text.
func (b *occurrenceBuilder) interpolations(lit *ast.StringLit) {
	text := b.idx.text
	start := lit.Loc.Offset
	if start < 0 || start >= len(text) || text[start] != '"' {
		return
	}
	end := start + 1
	for end < len(text) && text[end] != '"' && text[end] != '\n' {
		if text[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(text) || text[end] != '"' {
		return
	}
	content := text[start+1 : end]
	for i := 0; i < len(content); {
		if content[i] != '{' {
			i++
			continue
		}
		i++
		j := i
		for j < len(content) && (content[j] == ' ' || content[j] == '\t') {
			j++
		}
		if j < len(content) && isInterpolationIdentStart(content[j]) {
			k := j
			for k < len(content) && isInterpolationIdentContinue(content[k]) {
				k++
			}
			b.resolveValue(content[j:k], start+1+j, false)
		}
		// Skip the whole `{...}` so nested braces do not start new roots.
		for depth := 1; i < len(content) && depth > 0; i++ {
			switch content[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
	}
}

func isInterpolationIdentStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isInterpolationIdentContinue(ch byte) bool {
	return isInterpolationIdentStart(ch) || ch >= '0' && ch <= '9'
}

// dataTarget resolves the first argument of a data operation. Like the
// checker, a global model of that name wins over a binding.
func (b *occurrenceBuilder) dataTarget(x *ast.Ident) {
	if b.kindOf(x.Name) == "model" {
		b.add(x.Loc.Offset, x.Name, "model:"+x.Name, false)
		return
	}
	b.ident(x)
}

func (b *occurrenceBuilder) call(x *ast.FnCall) {
	switch {
	case dataOpNames[x.Name]:
		for i, a := range x.Args {
			if id, ok := a.(*ast.Ident); ok {
				if i == 0 {
					b.dataTarget(id)
					continue
				}
				if id.Name == "first" && b.tokenKind(id.Loc.Offset) == lexer.TokenFirst {
					continue
				}
			}
			b.expr(a)
		}
	case x.Name == "where":
		for _, a := range x.Args {
			b.wherePredicate(a)
		}
	case x.Name == "order" || x.Name == "with":
		// Bare arguments are column or relation names.
		for _, a := range x.Args {
			if _, ok := a.(*ast.Ident); !ok {
				b.expr(a)
			}
		}
	case x.Name == "call":
		for i, a := range x.Args {
			if id, ok := a.(*ast.Ident); ok {
				if key := normalizeExternal(id.Name); i == 0 && b.idx.externals[key].Raw != "" {
					b.add(id.Loc.Offset, id.Name, "external:"+key, false)
				}
				continue // service, then HTTP method
			}
			b.expr(a)
		}
	case x.Name == "emit" || x.Name == "enqueue":
		// Bare event, destination and queue names are symbolic, not bindings.
		for _, a := range x.Args {
			if _, ok := a.(*ast.Ident); !ok {
				b.expr(a)
			}
		}
	case x.Name == "log":
		// The message is an expression; a bare identifier after it is a level.
		for i, a := range x.Args {
			if _, ok := a.(*ast.Ident); ok && i > 0 {
				continue
			}
			b.expr(a)
		}
	case x.Name == "inject":
		for i, a := range x.Args {
			if id, ok := a.(*ast.Ident); ok && i == 1 {
				if off, ok := b.tokenOffset(id.Loc.Offset, id.Name); ok && !b.inMiddleware {
					b.bind(id.Name, off)
				}
				continue
			}
			b.expr(a)
		}
	case x.Name == "map":
		if len(x.Args) > 1 {
			if body, ok := x.Args[1].(*ast.FnCall); ok && dataOpNames[body.Name] && len(body.Args) > 0 {
				if id, ok := body.Args[0].(*ast.Ident); ok {
					b.idx.mapDerived[id.Name] = true
				}
			}
		}
		for i, a := range x.Args {
			if i == 1 {
				b.mapBody++
			}
			b.expr(a)
			if i == 1 {
				b.mapBody--
			}
		}
	case x.Name == "join" || x.Name == "leave" || x.Name == "broadcast" || x.Name == "whisper":
		for _, a := range x.Args {
			// The room/connection target is a FnCall. Its name is a model when
			// one of that name is declared, as the checker resolves it.
			if target, ok := a.(*ast.FnCall); ok {
				if b.kindOf(target.Name) == "model" {
					if off, ok := b.nameAfter(x.Loc, target.Name); ok {
						b.add(off, target.Name, "model:"+target.Name, false)
					}
				}
				for _, ta := range target.Args {
					b.expr(ta)
				}
				continue
			}
			b.expr(a)
		}
	case x.Name == "level" || x.Name == "event" || x.Name == "queue":
		return
	default:
		b.fnRef(x)
		for _, a := range x.Args {
			b.expr(a)
		}
	}
}

// wherePredicate walks a where(...) predicate. The left side of a comparison
// is a column of the target model, so a bare identifier there is not a
// binding reference. The right side is an ordinary expression.
func (b *occurrenceBuilder) wherePredicate(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Ident:
		// A bare name is a column unless a binding of that name is visible, in
		// which case the checker reads the binding.
		b.resolveValue(x.Name, x.Loc.Offset, true)
	case *ast.BinaryExpr:
		if x.Op == "and" || x.Op == "or" {
			b.wherePredicate(x.Left)
			b.wherePredicate(x.Right)
			return
		}
		if _, ok := x.Left.(*ast.Ident); !ok {
			b.expr(x.Left)
		}
		b.expr(x.Right)
	case *ast.ParenExpr:
		b.wherePredicate(x.Expr)
	case *ast.UnaryExpr:
		b.wherePredicate(x.Operand)
	default:
		b.expr(e)
	}
}

// fnRef resolves a call to a user fn or pipe, from `pipe name(...)` or `name(...)`.
func (b *occurrenceBuilder) fnRef(x *ast.FnCall) {
	kind := b.kindOf(x.Name)
	if kind != "fn" && kind != "pipe" {
		return
	}
	if b.tokenKind(x.Loc.Offset) == lexer.TokenPipe {
		if off, ok := b.nameAfter(x.Loc, x.Name); ok {
			b.add(off, x.Name, kind+":"+x.Name, false)
		}
		return
	}
	b.add(x.Loc.Offset, x.Name, kind+":"+x.Name, false)
}

// global walks non-statement content (types, constraints, meta, fields, KV
// values) by reflection. Expression, type, use and constraint nodes are
// resolved explicitly; statement lists are skipped and handled by topLevel.
func (b *occurrenceBuilder) global(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		if b.node(v.Interface()) {
			return
		}
		b.global(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() || skipInGlobalWalk(f.Type) || b.deferred[f.Type] {
				continue
			}
			b.global(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			b.global(v.Index(i))
		}
	}
}

// node resolves one special node. It reports whether the node was handled.
func (b *occurrenceBuilder) node(v any) bool {
	switch x := v.(type) {
	case ast.Expr:
		b.expr(x)
		return true
	case *ast.NamedType:
		switch kind := b.kindOf(x.Name); kind {
		case "model", "enum", "type":
			b.add(x.Loc.Offset, x.Name, kind+":"+x.Name, false)
		}
		return true
	case *ast.UseStmt:
		if b.kindOf(x.Name) == "middleware" {
			if off, ok := b.nameAfter(x.Loc, x.Name); ok {
				b.add(off, x.Name, "middleware:"+x.Name, false)
			}
		}
		for _, a := range x.Args {
			b.expr(a)
		}
		if x.Body != nil {
			b.global(reflect.ValueOf(x.Body))
		}
		return true
	case *ast.Constraint_:
		switch x.Kind {
		case "ref":
			// `ref(model)`: the parser anchors the Ident after the closing
			// paren, so the name is read from the tokens after `ref`.
			if id, ok := x.Value.(*ast.Ident); ok {
				if i, ok := b.idx.byOff[x.Loc.Offset]; ok && b.idx.toks[i].Kind == lexer.TokenRef && i+2 < len(b.idx.toks) {
					if t := b.idx.toks[i+2]; t.Value == id.Name && b.kindOf(id.Name) == "model" {
						b.add(t.Loc.Offset, id.Name, "model:"+id.Name, false)
					}
				}
			}
		case "format":
			// Format names are not symbols.
		default:
			b.expr(x.Value)
		}
		return true
	case *ast.Intent:
		return true
	}
	return false
}

// saveSchemaModel resolves `model <name>` inside a save schema block.
func (b *occurrenceBuilder) saveSchemaModel(n *ast.SaveSchema) {
	if n.Model == "" {
		return
	}
	i, ok := b.idx.byOff[n.Loc.Offset]
	if !ok {
		return
	}
	depth := 0
	for j := i; j+1 < len(b.idx.toks); j++ {
		t := b.idx.toks[j]
		switch t.Kind {
		case lexer.TokenLBrace:
			depth++
		case lexer.TokenRBrace:
			depth--
			if depth <= 0 && j > i {
				return
			}
		case lexer.TokenModel:
			if next := b.idx.toks[j+1]; depth > 0 && next.Value == n.Model {
				b.add(next.Loc.Offset, n.Model, "model:"+n.Model, false)
				return
			}
		}
	}
}

// fixtureSeedModel resolves `fixture "x" seed <model> {...}`.
func (b *occurrenceBuilder) fixtureSeedModel(n *ast.Fixture) {
	if n.SeedModel == "" {
		return
	}
	i, ok := b.idx.byOff[n.Loc.Offset]
	if !ok {
		return
	}
	for j := i; j+1 < len(b.idx.toks) && j < i+8; j++ {
		if b.idx.toks[j].Kind == lexer.TokenSeed {
			if next := b.idx.toks[j+1]; next.Value == n.SeedModel {
				b.add(next.Loc.Offset, n.SeedModel, "model:"+n.SeedModel, false)
			}
			return
		}
	}
}

// kindOf returns the top-level declaration kind for name, or "".
func (b *occurrenceBuilder) kindOf(name string) string {
	if g, ok := b.idx.globals[name]; ok {
		return g.Kind
	}
	return b.idx.imported[name].Kind
}

// declares reports whether name is a top-level declaration of this document or
// of another file in its include component.
func (s *symbolIndex) declares(name string) bool {
	if _, ok := s.globals[name]; ok {
		return true
	}
	_, ok := s.imported[name]
	return ok
}

func (b *occurrenceBuilder) tokenKind(off int) lexer.TokenKind {
	if i, ok := b.idx.byOff[off]; ok {
		return b.idx.toks[i].Kind
	}
	return lexer.TokenIllegal
}

// tokenOffset returns off when the token starting there is spelled name.
func (b *occurrenceBuilder) tokenOffset(off int, name string) (int, bool) {
	i, ok := b.idx.byOff[off]
	if !ok || b.idx.toks[i].Value != name {
		return 0, false
	}
	return off, true
}

// nameAfter returns the offset of the token following the token at loc, when
// that token is spelled name. It anchors names the AST does not locate.
func (b *occurrenceBuilder) nameAfter(loc lexer.Loc, name string) (int, bool) {
	i, ok := b.idx.byOff[loc.Offset]
	if !ok || i+1 >= len(b.idx.toks) {
		b.idx.mismatches++
		return 0, false
	}
	t := b.idx.toks[i+1]
	if t.Value != name {
		b.idx.mismatches++
		return 0, false
	}
	return t.Loc.Offset, true
}

// add records an occurrence when the token at off is spelled name.
func (b *occurrenceBuilder) add(off int, name, key string, decl bool) {
	if _, ok := b.tokenOffset(off, name); !ok {
		b.idx.mismatches++
		return
	}
	b.idx.occs = append(b.idx.occs, symOccurrence{Off: off, Len: len(name), Name: name, Key: key, Decl: decl})
}

// addRaw records an occurrence at a byte offset inside a token, such as an
// interpolation root in a string. The source must spell name there.
func (b *occurrenceBuilder) addRaw(off int, name, key string, decl bool) {
	if off < 0 || off >= len(b.idx.text) || !strings.HasPrefix(b.idx.text[off:], name) {
		b.idx.mismatches++
		return
	}
	b.idx.occs = append(b.idx.occs, symOccurrence{Off: off, Len: len(name), Name: name, Key: key, Decl: decl})
}

// at returns the occurrence containing byte offset off. A cursor placed just
// after a name also matches it.
func (s *symbolIndex) at(off int) (symOccurrence, bool) {
	for _, o := range s.occs {
		if off >= o.Off && off <= o.Off+o.Len {
			return o, true
		}
	}
	return symOccurrence{}, false
}

// occurrencesOf returns every occurrence of key in source order.
func (s *symbolIndex) occurrencesOf(key string) []symOccurrence {
	var out []symOccurrence
	for _, o := range s.occs {
		if o.Key == key {
			out = append(out, o)
		}
	}
	return out
}
