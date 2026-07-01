package imports

import "fmt"

import (
	alias "go/ast"
	_ "go/parser"
	. "go/token"

	"strings"
)

var _ = fmt.Sprint

var _ = strings.TrimSpace

var _ = alias.NewIdent
