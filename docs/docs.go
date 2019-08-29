// Package docs provides access to doc comments.
package docs

import (
	"github.com/mewspring/cc"
)

type Comment struct {
	Lit string
	Loc cc.Location
}

type DocComment struct {
	Decl    *cc.Node
	Comment Comment
}
