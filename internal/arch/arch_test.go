// Package arch holds no code. It holds the tests that keep the architecture
// from decaying.
//
// Every rule here was a real defect in the prototype. A design rule that is
// only written down is a rule that lasts until the first hurried afternoon;
// these fail the build instead.
package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/thirawat27/wut"

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root")
	return ""
}

// pkgFile is one parsed source file with its package path.
type pkgFile struct {
	Path    string // repo-relative
	PkgPath string // import path relative to the module
	File    *ast.File
	Fset    *token.FileSet
}

// loadAll parses every Go file in the module.
func loadAll(t *testing.T) []pkgFile {
	t.Helper()
	root := repoRoot(t)
	var out []pkgFile

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "build", "dist", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		out = append(out, pkgFile{
			Path:    rel,
			PkgPath: modulePath + "/" + filepath.ToSlash(filepath.Dir(rel)),
			File:    f,
			Fset:    fset,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 10 {
		t.Fatalf("only found %d source files; the walk is wrong", len(out))
	}
	return out
}

func imports(f pkgFile) []string {
	var out []string
	for _, imp := range f.File.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err == nil {
			out = append(out, p)
		}
	}
	return out
}

func isTest(f pkgFile) bool { return strings.HasSuffix(f.Path, "_test.go") }

// I4 — internal/core is pure.
//
// This is the rule that makes the domain testable as values. The prototype had
// no such boundary: cmd/ constructed storage in twelve files and a config
// singleton was read by seventeen, so nothing below the CLI could be tested
// without a real database and a real config file on disk. Five of eighteen
// packages had tests.
func TestCoreIsPure(t *testing.T) {
	forbidden := map[string]string{
		"os":            "the domain must not touch the filesystem or the environment",
		"os/exec":       "the domain must never be able to start a process",
		"net":           "the domain must not reach the network",
		"net/http":      "the domain must not reach the network",
		"io/fs":         "the domain must not read the filesystem",
		"path/filepath": "filesystem paths belong to an adapter",
	}
	for _, f := range loadAll(t) {
		if !strings.Contains(f.PkgPath, "/internal/core/") || isTest(f) {
			continue
		}
		for _, imp := range imports(f) {
			if reason, bad := forbidden[imp]; bad {
				t.Errorf("%s imports %q: %s", f.Path, imp, reason)
			}
			if strings.Contains(imp, "/internal/adapter/") {
				t.Errorf("%s imports an adapter (%s): core must not know how anything is implemented", f.Path, imp)
			}
			if strings.Contains(imp, "/internal/app") || strings.Contains(imp, "/internal/cli") {
				t.Errorf("%s imports %s: the domain must not depend on a layer above it", f.Path, imp)
			}
		}
	}
}

// The use-case layer talks to ports, never to adapters. This is what lets the
// daemon serve the same use cases the CLI runs in-process.
func TestAppDependsOnPortsNotAdapters(t *testing.T) {
	for _, f := range loadAll(t) {
		if !strings.HasPrefix(f.PkgPath, modulePath+"/internal/app") || isTest(f) {
			continue
		}
		for _, imp := range imports(f) {
			if strings.Contains(imp, "/internal/adapter/") {
				t.Errorf("%s imports %s: app must depend on internal/port instead", f.Path, imp)
			}
			if strings.Contains(imp, "/internal/cli") || strings.Contains(imp, "/internal/daemon") {
				t.Errorf("%s imports %s: a use case must not depend on its transport", f.Path, imp)
			}
		}
	}
}

// The CLI receives adapters; it does not construct them. cmd/wut is the single
// construction site.
func TestCLIDoesNotConstructAdapters(t *testing.T) {
	allowed := map[string]bool{
		modulePath + "/internal/adapter/render": true, // presentation is the CLI's own job
	}
	for _, f := range loadAll(t) {
		if !strings.HasPrefix(f.PkgPath, modulePath+"/internal/cli") || isTest(f) {
			continue
		}
		for _, imp := range imports(f) {
			if strings.Contains(imp, "/internal/adapter/") && !allowed[imp] {
				t.Errorf("%s imports %s: the CLI is handed its adapters by cmd/wut", f.Path, imp)
			}
		}
	}
}

// I2 — only two packages may start a process.
//
// This is the enforcement point for the invariant that WUT never runs the
// user's command. The prototype's `oops` re-ran it to harvest stderr, which
// meant `oops` after `git push` pushed again, and after `docker system prune
// -af` pruned again. A deny-list of dangerous commands cannot fix that;
// removing the ability to execute can.
func TestOnlyTwoPackagesMayExec(t *testing.T) {
	allowed := map[string]bool{
		modulePath + "/internal/adapter/facts": true, // allowlisted read-only probes
		modulePath + "/internal/adapter/model": true, // supervises a downloaded model runtime
		modulePath + "/internal/daemon":        true, // supervises the model process
	}
	for _, f := range loadAll(t) {
		if isTest(f) || allowed[f.PkgPath] {
			continue
		}
		for _, imp := range imports(f) {
			if imp == "os/exec" {
				t.Errorf("%s imports os/exec. Only the fact prober and the model supervisor may start a process; "+
					"if this is a new legitimate case, add it to the allowlist in this test and say why", f.Path)
			}
		}
	}
}

// The fact prober is the one place os/exec is reachable from a user-facing
// path, so its allowlist must be compared element for element. A prefix match
// would let a longer argv smuggle arguments in behind an allowed prefix.
func TestFactProbeAllowlistIsExact(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "internal", "adapter", "facts", "facts.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "func probeAllowed(") {
		t.Fatal("probeAllowed is gone: the allowlist is the only thing standing between WUT and running arbitrary commands")
	}
	for _, bad := range []string{"strings.HasPrefix(name", "strings.HasPrefix(args"} {
		if strings.Contains(src, bad) {
			t.Errorf("the allowlist uses %s, which is a prefix match; it must compare argv element for element", bad)
		}
	}
}

// I5 — no package-level mutable state.
//
// The prototype config singleton is the reason this rule exists: seventeen
// files read a global, so nothing could be tested with a different
// configuration and nothing could hold two at once.
func TestNoPackageLevelMutableState(t *testing.T) {
	// Exempt: platform packages cache immutable process facts, and cmd/ is the
	// composition root, where `version` has to be a package-level var because
	// the release pipeline sets it with -ldflags.
	allowedPkg := func(p string) bool {
		return strings.Contains(p, "/internal/platform/") || strings.HasPrefix(p, modulePath+"/cmd/")
	}
	for _, f := range loadAll(t) {
		if isTest(f) || allowedPkg(f.PkgPath) {
			continue
		}
		for _, decl := range f.File.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if name.Name == "_" || isImmutableVar(vs, name.Name) {
						continue
					}
					pos := f.Fset.Position(name.Pos())
					t.Errorf("%s:%d: package-level var %q. Mutable package state is what made the prototype untestable; "+
						"pass it in instead", f.Path, pos.Line, name.Name)
				}
			}
		}
	}
}

// isImmutableVar allows the declarations that are constants Go will not let us
// write as const: embedded files, compiled regexes, sentinel errors, and
// lookup tables that are never written after init.
func isImmutableVar(vs *ast.ValueSpec, name string) bool {
	switch {
	case strings.HasPrefix(name, "Err"), strings.HasPrefix(name, "err"):
		return true // sentinel errors
	case strings.HasSuffix(name, "Re"), strings.HasSuffix(name, "Regexp"):
		return true
	}
	// go:embed targets and table literals.
	if vs.Doc != nil {
		for _, c := range vs.Doc.List {
			if strings.Contains(c.Text, "go:embed") {
				return true
			}
		}
	}
	for _, v := range vs.Values {
		switch e := v.(type) {
		case *ast.CompositeLit:
			return true // a table
		case *ast.CallExpr:
			if fn, ok := e.Fun.(*ast.SelectorExpr); ok {
				pkg, _ := fn.X.(*ast.Ident)
				switch {
				case pkg == nil:
				case pkg.Name == "errors", pkg.Name == "regexp", pkg.Name == "fmt",
					pkg.Name == "crc32", pkg.Name == "strings", pkg.Name == "sync":
					// Immutable lookup tables and replacers, built once at init.
					return true
				}
			}
		}
	}
	// A bare `var x []byte` with no value is almost always a go:embed target
	// whose comment sits above the directive rather than the spec.
	return len(vs.Values) == 0
}

// pkg/ is the public contract. It must not drag internals into a consumer's
// build beyond the types it deliberately re-exports.
func TestPublicPackageStaysThin(t *testing.T) {
	for _, f := range loadAll(t) {
		if !strings.HasPrefix(f.PkgPath, modulePath+"/pkg/") || isTest(f) {
			continue
		}
		for _, imp := range imports(f) {
			if strings.Contains(imp, "/internal/adapter/") || strings.Contains(imp, "/internal/app") ||
				strings.Contains(imp, "/internal/cli") {
				t.Errorf("%s imports %s: the public schema must not depend on internals that can change", f.Path, imp)
			}
		}
	}
}

// Nothing below main may end the process. The prototype called os.Exit inside
// a Cobra pre-run hook, which skipped the post-run hook and every pending
// defer, including closing the database.
func TestOnlyMainMayExit(t *testing.T) {
	for _, f := range loadAll(t) {
		if isTest(f) || f.PkgPath == modulePath+"/cmd/wut" {
			continue
		}
		ast.Inspect(f.File, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" || sel.Sel.Name != "Exit" {
				return true
			}
			pos := f.Fset.Position(call.Pos())
			t.Errorf("%s:%d: os.Exit outside main. Return an error carrying an exit code instead, "+
				"so deferred cleanup still runs", f.Path, pos.Line)
			return true
		})
	}
}
