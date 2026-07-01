package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mitchell-wallace/rally/tools/archguard/policy"
)

// scanRepo walks root and returns a FileInfo for every non-generated,
// non-skipped `.go` file, so the policy engine sees only the production and
// test sources that budgets and boundaries apply to.
//
// It skips, without descending: directories named "testdata" or "vendor", the
// build-output directory "bin", and any hidden directory (name starting with
// "."), which covers ".git", ".rally", and ".laps". It also skips files that
// begin with a "// Code generated" marker, which are exempt from every budget.
func scanRepo(root string) ([]policy.FileInfo, error) {
	var files []policy.FileInfo
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := relSlash(root, path)
		if err != nil {
			return err
		}
		fi, skip, err := scanFile(path, rel)
		if err != nil {
			return fmt.Errorf("scan %s: %w", rel, err)
		}
		if skip {
			return nil
		}
		files = append(files, fi)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// shouldSkipDir reports whether a directory (by base name) must not be walked.
func shouldSkipDir(name string) bool {
	switch name {
	case "testdata", "vendor", "bin":
		return true
	}
	// Hidden bookkeeping/build dirs: ".git", ".rally", ".laps", editor dirs, …
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

// scanFile reads a single `.go` file and builds its FileInfo. It returns
// skip=true (and a zero FileInfo) when the file is generated and should be
// excluded from the scan entirely.
func scanFile(path, rel string) (fi policy.FileInfo, skip bool, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return policy.FileInfo{}, false, err
	}
	if isGeneratedSource(src) {
		return policy.FileInfo{}, true, nil
	}
	imports, err := parseImports(rel, src)
	if err != nil {
		return policy.FileInfo{}, false, err
	}
	return policy.FileInfo{
		Path:    rel,
		Package: slashDir(rel),
		IsTest:  strings.HasSuffix(rel, "_test.go"),
		Lines:   countLines(src),
		Imports: imports,
	}, false, nil
}

// countLines returns the physical line count: the number of newline bytes,
// which matches what `wc -l` reports (a final line without a trailing newline
// is not counted, exactly as `wc -l` does not count it).
func countLines(src []byte) int {
	return bytes.Count(src, []byte{'\n'})
}

// isGeneratedSource reports whether the source is a generated file, i.e. a line
// beginning with "// Code generated" appears before the package clause. This
// mirrors the Go toolchain's convention closely enough for the exemption while
// matching design.md's "files beginning with // Code generated" wording.
func isGeneratedSource(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "// Code generated") {
			return true
		}
		// Stop at the package clause: markers only count before it.
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

// parseImports parses only the import section of the source and returns each
// import path with any alias or dot prefix stripped. Aliased imports
// (`x "path"`), dot imports (`. "path"`), blank imports (`_ "path"`), and
// grouped import blocks are all handled by go/parser; we only keep the paths.
func parseImports(rel string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("bad import path %s: %w", spec.Path.Value, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// relSlash returns path relative to root using forward slashes.
func relSlash(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// slashDir returns the directory of a slash-separated repo-relative path, or
// "." for a top-level file.
func slashDir(rel string) string {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return "."
	}
	return rel[:i]
}
