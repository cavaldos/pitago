// Package filetree is the neo-tree style file sidebar of /focus: a folding
// directory tree of the working directory with git status marks.
//
// Inside a git work tree the file list comes from `git ls-files` (tracked +
// untracked, .gitignore respected), so build output and node_modules stay
// out. Elsewhere directories are read lazily from disk, skipping .git and
// node_modules.
//
// Everything here is free of TUI state: the sidebar wiring lives in src/app.
package filetree

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Node is one file or directory.
type Node struct {
	Name   string
	Path   string // slash-separated, relative to the tree base
	Dir    bool
	Open   bool
	Status string // git mark: "M" "A" "D" "R" "U" "?" ("" clean); dirs roll up their files
	Kids   []*Node
	parent *Node
	loaded bool
}

// Parent is the node's directory (nil for the root).
func (n *Node) Parent() *Node { return n.parent }

// Tree is the whole sidebar model.
type Tree struct {
	Base  string
	Git   bool
	Root  *Node
	index map[string]*Node // path -> node, git mode (built eagerly)
}

// Row is one visible line of the tree.
type Row struct {
	N     *Node
	Depth int
}

var skipDirs = map[string]bool{".git": true, "node_modules": true}

// gitTimeout keeps a slow or hung git from freezing the window.
const gitTimeout = 3 * time.Second

// Load builds the tree for base ("" = ".").
func Load(base string) *Tree {
	if base == "" {
		base = "."
	}
	t := &Tree{Base: base, Root: &Node{Dir: true, Open: true, loaded: true}}
	if files, ok := gitFiles(base); ok {
		t.Git = true
		t.index = map[string]*Node{"": t.Root}
		for _, f := range files {
			t.add(f)
		}
		sortAll(t.Root)
		t.applyStatus(ParseStatus(gitOut(base, "-c", "color.status=false", "-c", "core.quotePath=false",
			"status", "--short", "--untracked-files=all", "--", ".")))
		return t
	}
	t.Root.loaded = false
	t.ensure(t.Root)
	return t
}

// Reload rebuilds the tree from disk, keeping the open directories open.
func Reload(old *Tree) *Tree {
	t := Load(old.Base)
	var open []string
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, k := range n.Kids {
			if k.Dir && k.Open {
				open = append(open, k.Path)
				walk(k)
			}
		}
	}
	walk(old.Root)
	for _, p := range open {
		if n := t.Reveal(p); n != nil {
			t.Expand(n)
		}
	}
	return t
}

func gitOut(base string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", base}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitFiles lists files under base via git; ok=false outside a work tree.
func gitFiles(base string) ([]string, bool) {
	if strings.TrimSpace(gitOut(base, "rev-parse", "--is-inside-work-tree")) != "true" {
		return nil, false
	}
	out := gitOut(base, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(out, "\x00") {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	return files, true
}

// add inserts a file path, creating its directories (git mode).
func (t *Tree) add(p string) {
	parts := strings.Split(p, "/")
	cur := t.Root
	for i, name := range parts {
		sub := strings.Join(parts[:i+1], "/")
		if n, ok := t.index[sub]; ok {
			cur = n
			continue
		}
		n := &Node{Name: name, Path: sub, Dir: i < len(parts)-1, parent: cur, loaded: true}
		cur.Kids = append(cur.Kids, n)
		t.index[sub] = n
		cur = n
	}
}

// ensure reads a directory's children from disk (lazy mode only).
func (t *Tree) ensure(n *Node) {
	if n.loaded || !n.Dir {
		return
	}
	n.loaded = true
	ents, err := os.ReadDir(filepath.Join(t.Base, filepath.FromSlash(n.Path)))
	if err != nil {
		return
	}
	for _, e := range ents {
		if skipDirs[e.Name()] {
			continue
		}
		dir := e.IsDir()
		if !dir && e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(t.Base, filepath.FromSlash(n.Path), e.Name())); err == nil && st.IsDir() {
				dir = true
			}
		}
		n.Kids = append(n.Kids, &Node{Name: e.Name(), Path: path.Join(n.Path, e.Name()), Dir: dir, parent: n})
	}
	sortKids(n)
}

func sortKids(n *Node) {
	sort.SliceStable(n.Kids, func(i, j int) bool {
		a, b := n.Kids[i], n.Kids[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func sortAll(n *Node) {
	sortKids(n)
	for _, k := range n.Kids {
		if k.Dir {
			sortAll(k)
		}
	}
}

// ParseStatus reads `git status --short` output (paths relative to the
// directory git ran in) into path -> one-letter mark.
func ParseStatus(out string) map[string]string {
	marks := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		if len(ln) < 4 {
			continue
		}
		xy, p := ln[:2], ln[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:] // rename: the new name is what exists now
		}
		if strings.HasPrefix(p, "\"") {
			if u, err := strconv.Unquote(p); err == nil {
				p = u
			}
		}
		p = strings.TrimSuffix(p, "/")
		if p == "" || strings.HasPrefix(p, "../") {
			continue
		}
		var code string
		switch {
		case xy == "??":
			code = "?"
		case xy[0] == 'U' || xy[1] == 'U' || xy == "AA" || xy == "DD":
			code = "U"
		case xy[1] != ' ':
			code = string(xy[1])
		default:
			code = string(xy[0])
		}
		if code == "C" {
			code = "A"
		}
		marks[p] = code
	}
	return marks
}

// applyStatus marks files and rolls the marks up to their directories: a
// directory takes its files' mark when they agree, "M" when they differ.
func (t *Tree) applyStatus(marks map[string]string) {
	for p, code := range marks {
		n, ok := t.index[p]
		if !ok {
			continue
		}
		n.Status = code
		for d := n.parent; d != nil && d != t.Root; d = d.parent {
			switch d.Status {
			case "":
				d.Status = code
			case code:
			default:
				d.Status = "M"
			}
		}
	}
}

// Rows flattens the visible part of the tree (the root itself is hidden).
func (t *Tree) Rows() []Row {
	var out []Row
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		for _, k := range n.Kids {
			out = append(out, Row{N: k, Depth: depth})
			if k.Dir && k.Open {
				walk(k, depth+1)
			}
		}
	}
	walk(t.Root, 0)
	return out
}

// Expand opens a directory (reading it first in lazy mode).
func (t *Tree) Expand(n *Node) {
	if n == nil || !n.Dir {
		return
	}
	t.ensure(n)
	n.Open = true
}

// Collapse closes a directory.
func (t *Tree) Collapse(n *Node) {
	if n != nil && n.Dir {
		n.Open = false
	}
}

// Toggle flips a directory open/closed.
func (t *Tree) Toggle(n *Node) {
	if n == nil || !n.Dir {
		return
	}
	if n.Open {
		t.Collapse(n)
	} else {
		t.Expand(n)
	}
}

// Reveal opens every directory above rel (slash path under Base) and
// returns its node, or nil when rel is not in the tree.
func (t *Tree) Reveal(rel string) *Node {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return nil
	}
	cur := t.Root
	for _, name := range strings.Split(rel, "/") {
		t.ensure(cur)
		var next *Node
		for _, k := range cur.Kids {
			if k.Name == name {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		if cur != t.Root {
			cur.Open = true
		}
		cur = next
	}
	return cur
}

// IndexOf finds a node's row, -1 when hidden or absent.
func IndexOf(rows []Row, n *Node) int {
	for i, r := range rows {
		if r.N == n {
			return i
		}
	}
	return -1
}
