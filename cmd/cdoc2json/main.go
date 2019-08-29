// cdoc2json -clang_args="-m32|-I./include|-I/usr/lib/clang/8.0.1/include" foo.h

// clang++ -Wp,-v -x c++ - -fsyntax-only < /dev/null 2>&1 | grep /clang/

package main

import (
	"flag"
	"fmt"
	"go/scanner"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/go-clang/clang-v3.9/clang"
	"github.com/mewkiz/pkg/jsonutil"
	"github.com/mewkiz/pkg/term"
	"github.com/mewspring/cc"
	"github.com/mewspring/cdoc2json/docs"
	"github.com/pkg/errors"
)

var (
	// dbg is a logger with the "cdoc2json:" prefix which logs debug messages to
	// standard error.
	dbg = log.New(os.Stderr, term.CyanBold("cdoc2json:")+" ", 0)
	// warn is a logger with the "cdoc2json:" prefix which logs warning messages
	// to standard error.
	warn = log.New(os.Stderr, term.RedBold("cdoc2json:")+" ", 0)
)

func main() {
	// Parse command line arguments.
	var (
		// Output path for doc comments JSON file.
		output string
		// Clang arguments.
		clangArgs []string
	)
	var clangArgsRaw string
	flag.StringVar(&output, "output", "doc_comments.json", "output path for doc comments JSON file")
	flag.StringVar(&clangArgsRaw, "clang_args", "", "pipe-separated Clang arguments")
	flag.Parse()
	clangArgs = strings.Split(clangArgsRaw, "|")
	// map from identifier to comment.
	commentFromIdent := make(map[string]string)
	for _, srcPath := range flag.Args() {
		if err := parse(srcPath, commentFromIdent, clangArgs...); err != nil {
			log.Fatalf("%+v", err)
		}
	}
	dbg.Printf("creating %q", output)
	if err := jsonutil.WriteFile(output, commentFromIdent); err != nil {
		log.Fatalf("%+v", err)
	}
}

func parse(srcPath string, commentFromIdent map[string]string, clangArgs ...string) error {
	dbg.Printf("parsing %q", srcPath)
	comments, err := parseComments(srcPath)
	if err != nil {
		return errors.WithStack(err)
	}
	// Merge consequtive line comments.
	comments = mergeLineComments(comments)
	file, err := cc.ParseFile(srcPath, clangArgs...)
	if err != nil {
		return errors.WithStack(err)
	}
	defer file.Close()
	decls := findDecls(file.Root)
	docComments := addDocComments(decls, comments)
	for _, docComment := range docComments {
		ident := docComment.Decl.Body.Spelling()
		new := docComment.Comment.Lit
		if old, ok := commentFromIdent[ident]; ok {
			warn.Printf("doc comment for %q already present; old %q, new %q", ident, old, new)
		}
		commentFromIdent[ident] = new
	}
	//printDocComments(docComments)
	return nil
}

func printDocComments(docComments []docs.DocComment) {
	for _, docComment := range docComments {
		fmt.Println(docComment.Decl.Body.Spelling())
		fmt.Println(docComment.Comment.Lit)
	}
}

func mergeLineComments(comments []docs.Comment) []docs.Comment {
	var new []docs.Comment
	for i := 0; i < len(comments); i++ {
		a := comments[i]
		for j := i + 1; j < len(comments); j++ {
			b := comments[j]
			if isConsequtiveLineComments(a, b) {
				a = mergeLineComment(a, b)
				i++
			}
		}
		new = append(new, a)
	}
	return new
}

func isConsequtiveLineComments(a, b docs.Comment) bool {
	if !strings.HasPrefix(a.Lit, "//") {
		return false
	}
	if !strings.HasPrefix(b.Lit, "//") {
		return false
	}
	return a.Loc.Line+uint32(strings.Count(a.Lit, "\n")) == b.Loc.Line-1
}

func mergeLineComment(a, b docs.Comment) docs.Comment {
	a.Lit += "\n" + b.Lit
	return a
}

func addDocComments(decls []*cc.Node, comments []docs.Comment) []docs.DocComment {
	var docComments []docs.DocComment
	i := 0 // current comment index.
loop:
	for _, decl := range decls {
		for i < len(comments) {
			comment := comments[i]
			commEndLoc := comment.Loc
			commEndLoc.Line += uint32(strings.Count(comment.Lit, "\n"))
			if less(decl.Loc, commEndLoc) {
				// skip decl, decl before comment.
				continue loop
			}
			if decl.Loc.Line-commEndLoc.Line <= 1 {
				// doc comment.
				docComment := docs.DocComment{
					Decl:    decl,
					Comment: comment,
				}
				docComments = append(docComments, docComment)
			}
			i++
		}
	}
	return docComments
}

func less(a, b cc.Location) bool {
	switch {
	case a.Line < b.Line:
		return true
	case a.Line > b.Line:
		return false
	}
	// case a.Line == b.Line:
	return a.Col < b.Col
}

func findDecls(root *cc.Node) []*cc.Node {
	var decls []*cc.Node
	visit := func(n *cc.Node) {
		switch n.Body.Kind() {
		case clang.Cursor_VarDecl, clang.Cursor_FunctionDecl:
			decls = append(decls, n)
		}
	}
	cc.Walk(root, visit)
	// sort decls.
	sort.Slice(decls, func(i, j int) bool {
		return less(decls[i].Loc, decls[j].Loc)
	})
	return decls
}

func parseComments(srcPath string) ([]docs.Comment, error) {
	src, err := ioutil.ReadFile(srcPath)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var comments []docs.Comment
	fset := token.NewFileSet()
	file := fset.AddFile(srcPath, 1, len(src))
	s := &scanner.Scanner{}
	eh := func(pos token.Position, msg string) {
		if msg == "illegal character U+0023 '#'" {
			// Ignore pre-process directives.
			return
		}
		warn.Printf("pos: %v, msg: %v", pos, msg)
	}
	s.Init(file, src, eh, scanner.ScanComments)
	for {
		p, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		pos := fset.Position(p)
		//dbg.Printf("pos: %v, tok: %v, lit: %v", pos, tok, lit)
		if tok == token.COMMENT {
			loc := cc.Location{
				File: pos.Filename,
				Line: uint32(pos.Line),
				Col:  uint32(pos.Column),
			}
			comment := docs.Comment{
				Lit: lit,
				Loc: loc,
			}
			comments = append(comments, comment)
		}
	}
	// TODO: remove, should not be needed as scanner results are already sorted.
	sort.Slice(comments, func(i, j int) bool {
		return less(comments[i].Loc, comments[j].Loc)
	})
	return comments, nil
}
