// Command doctidy rewrites the doc comments oapi-codegen copies from the spec into
// Go doc style: {@link X} (JSDoc) becomes X, and an em dash becomes a hyphen. Only
// comment lines are touched. Run by `go generate` after oapi-codegen.
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
)

var jsdocLink = regexp.MustCompile(`\{@link\s+([^}|]+?)\s*(?:\|\s*([^}]+?)\s*)?\}`)

func tidy(src []byte) []byte {
	lines := bytes.Split(src, []byte("\n"))
	for i, l := range lines {
		if !bytes.HasPrefix(bytes.TrimLeft(l, " \t"), []byte("//")) {
			continue
		}
		l = jsdocLink.ReplaceAllFunc(l, func(m []byte) []byte {
			sub := jsdocLink.FindSubmatch(m)
			if len(sub[2]) > 0 {
				return sub[2]
			}
			return sub[1]
		})
		l = bytes.ReplaceAll(l, []byte("—"), []byte("-"))
		lines[i] = l
	}
	return bytes.Join(lines, []byte("\n"))
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: doctidy <file.go>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], tidy(src), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
