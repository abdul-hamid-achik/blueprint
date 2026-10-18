package ast

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// trivialFieldNames are source bookkeeping fields, not syntax. StructureDump
// skips them so two files that differ only in layout compare equal.
var trivialFieldNames = map[string]bool{
	"Loc":          true,
	"Comments":     true,
	"SourceTokens": true,
	"BlankBefore":  true,
	"AnchorToken":  true,
}

var locType = reflect.TypeOf(lexer.Loc{})

// StructureDump renders the syntax tree of f as deterministic text with source
// locations, comments and layout trivia removed. Equal dumps mean equal syntax,
// so the formatter can use this to prove a rewrite did not change meaning.
func StructureDump(f *File) string {
	var sb strings.Builder
	dumpValue(&sb, reflect.ValueOf(f), 0)
	return sb.String()
}

// SameStructure reports whether a and b have identical structure, ignoring
// source positions and trivia. When they differ, the second result names the
// first differing line of the dumps.
func SameStructure(a, b *File) (bool, string) {
	da := StructureDump(a)
	db := StructureDump(b)
	if da == db {
		return true, ""
	}
	la := strings.Split(da, "\n")
	lb := strings.Split(db, "\n")
	for i := 0; i < len(la) || i < len(lb); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return false, fmt.Sprintf("line %d: %q != %q", i+1, x, y)
		}
	}
	return false, "dumps differ"
}

func dumpValue(sb *strings.Builder, v reflect.Value, depth int) {
	if depth > 500 {
		sb.WriteString("<too deep>")
		return
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			sb.WriteString("nil")
			return
		}
		sb.WriteString("&")
		dumpValue(sb, v.Elem(), depth+1)
	case reflect.Interface:
		if v.IsNil() {
			sb.WriteString("nil")
			return
		}
		dumpValue(sb, v.Elem(), depth+1)
	case reflect.Struct:
		if v.Type() == locType {
			return
		}
		t := v.Type()
		sb.WriteString(t.Name())
		sb.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if trivialFieldNames[f.Name] {
				continue
			}
			sb.WriteString(f.Name)
			sb.WriteString(":")
			dumpValue(sb, v.Field(i), depth+1)
			sb.WriteString(" ")
		}
		sb.WriteString("}")
	case reflect.Slice, reflect.Array:
		// nil and empty slices are the same syntax, and both dump as [].
		sb.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			dumpValue(sb, v.Index(i), depth+1)
			sb.WriteString(",")
		}
		sb.WriteString("]")
	case reflect.String:
		fmt.Fprintf(sb, "%q", v.String())
	default:
		fmt.Fprintf(sb, "%v", v.Interface())
	}
}
