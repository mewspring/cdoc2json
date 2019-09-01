package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/go-clang/clang-v3.9/clang"
	"github.com/kr/pretty"
	"github.com/mewkiz/pkg/jsonutil"
	"github.com/mewkiz/pkg/term"
	"github.com/mewspring/cc"
	"github.com/pkg/errors"
)

var (
	// dbg is a logger with the "addcdocs:" prefix which logs debug messages to
	// standard error.
	dbg = log.New(os.Stderr, term.CyanBold("addcdocs:")+" ", 0)
	// warn is a logger with the "addcdocs:" prefix which logs warning messages
	// to standard error.
	warn = log.New(os.Stderr, term.RedBold("addcdocs:")+" ", 0)
)

func usage() {
	const use = `
Usage:

	addcdocs [OPTION]... FILE.json

Flags:`
	fmt.Fprintln(os.Stderr, use[1:])
	flag.PrintDefaults()
}

func main() {
	// Parse comments.
	var (
		// doc comments JSON path.
		jsonPath string
		// Clang arguments.
		clangArgs []string
	)
	var clangArgsRaw string
	flag.StringVar(&jsonPath, "json_path", "doc_comments.json", "doc comments JSON path")
	flag.StringVar(&clangArgsRaw, "clang_args", "", "pipe-separated Clang arguments")
	flag.Parse()
	clangArgs = strings.Split(clangArgsRaw, "|")
	srcPaths := flag.Args()

	// Parse doc comments JSON file.
	docComments, err := parseDocComments(jsonPath)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	pretty.Println("docComments:", docComments)

	// Parse source files.
	for _, srcPath := range srcPaths {
		srcFile, err := parseSourceFile(srcPath, clangArgs)
		if err != nil {
			warn.Printf("%+v", err)
			// continue with partial AST.
		}
		if src, change := addComments(srcFile, docComments); change {
			dbg.Printf("adding comments to %q", srcPath)
			if err := ioutil.WriteFile(srcPath, []byte(src), 0644); err != nil {
				log.Fatalf("%+v", errors.WithStack(err))
			}
		}
	}
}

// addComments and doc comments to the given source file, connecting identifiers
// in the source code with the associated doc comments, as recorded in
// docComments, which maps from identifier to doc comment.
func addComments(srcFile *SourceFile, docComments map[string]string) ([]byte, bool) {
	oldSrc := string(srcFile.Buf)
	lines := strings.Split(oldSrc, "\n")
	// comments maps from line number to comment.
	comments := make(map[uint32]string)
	decls := findGlobalDecls(srcFile.File)
	for _, decl := range decls {
		if comment, ok := docComments[decl.Body.Spelling()]; ok {
			comment = normalizeComment(comment)
			comments[decl.Loc.Line] = comment
		}
	}
	newSrc := &strings.Builder{}
	for i, line := range lines {
		lineNr := uint32(i + 1)
		if comment, ok := comments[lineNr]; ok {
			fmt.Fprintf(newSrc, "%s\n", comment)
		}
		fmt.Fprintf(newSrc, "%s\n", line)
	}
	change := oldSrc != newSrc.String()
	return []byte(newSrc.String()), change
}

func normalizeComment(comment string) string {
	lines := strings.Split(comment, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "///") {
			// strip one trailing slash of ///.
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

func insert(ss []string, pos int, s string) []string {
	new := append(ss[:pos:pos], s)
	return append(new, ss[pos:]...)
}

func findGlobalDecls(file *cc.File) []*cc.Node {
	var decls []*cc.Node
	root := file.Root
	for _, child := range root.Children {
		if child.Body.Kind() == clang.Cursor_Namespace {
			root = child
		}
	}
	// TODO: handle namespaces.
	for _, child := range root.Children {
		switch child.Body.Kind() {
		case clang.Cursor_VarDecl, clang.Cursor_FunctionDecl:
			decls = append(decls, child)
		}
	}
	return decls
}

type SourceFile struct {
	Path string
	Buf  []byte
	File *cc.File
}

func parseSourceFile(srcPath string, clangArgs []string) (*SourceFile, error) {
	srcFile := &SourceFile{
		Path: srcPath,
	}
	buf, err := ioutil.ReadFile(srcPath)
	if err != nil {
		return srcFile, errors.WithStack(err)
	}
	srcFile.Buf = buf
	file, err := cc.ParseFile(srcPath, clangArgs...)
	srcFile.File = file // return partial results.
	if err != nil {
		return srcFile, errors.WithStack(err)
	}
	return srcFile, nil
}

func parseDocComments(jsonPath string) (map[string]string, error) {
	// docComments maps from identifier to doc comment.
	docComments := make(map[string]string)
	if err := jsonutil.ParseFile(jsonPath, &docComments); err != nil {
		return nil, errors.WithStack(err)
	}
	return docComments, nil
}
