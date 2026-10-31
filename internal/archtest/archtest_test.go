package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modPrefix = "github.com/jorben/tacet/internal/"

// The domain layer (module design 2.1).
var domain = []string{
	"workspace", "snapshot", "jobs", "lineage", "sched", "retention",
	"preflight", "extract", "detect", "rules", "findings", "coverage",
	"policy", "exceptions", "review", "plan", "preview",
	"export", "validate", "verdict", "publish", "report",
	"config", "registry", "pluginmgr", "envs", "models",
}

func plus(base []string, more ...string) []string {
	return append(append([]string{}, base...), more...)
}

func without(base []string, drop string) []string {
	var out []string
	for _, m := range base {
		if m != drop {
			out = append(out, m)
		}
	}
	return out
}

// allowed lists the internal packages each module may import, besides kernel
// (module design 2.3). Pure-logic modules (lineage, verdict) get kernel only.
// A module may use store for its own tables; stage modules may read the run
// snapshot and implement sched.StageRunner.
var allowed = map[string][]string{
	"kernel": {},
	"gui":    {"app", "query"},
	"cli":    {"app", "query"},
	// app is the composition root: besides the domain it wires the process, resource and
	// key infrastructure that no domain module may reach (documented deviation from 2.3).
	"app":   plus(domain, "store", "update", "host", "refs", "keys", "netgate", "platform", "lifecycle", "retention"),
	"query": plus(domain, "store"),
	// lifecycle runs the start-up and exit orders across these modules.
	"lifecycle": {"app", "workspace", "jobs", "sched", "host", "publish", "review", "retention", "platform"},
	// update reaches the exit order through an injected interface, not lifecycle.
	"update":     {"netgate", "envs", "registry", "query"},
	"workspace":  {"store", "keys", "platform"},
	"snapshot":   {"config", "registry", "models", "store"},
	"jobs":       plus(without(domain, "jobs"), "store"),
	"lineage":    {},
	"sched":      {"netgate", "platform"},
	"retention":  {"keys", "registry", "publish", "snapshot", "store"},
	"preflight":  {"workspace", "sched", "snapshot", "host", "refs", "store", "registry", "models"},
	"extract":    {"workspace", "sched", "snapshot", "host", "refs", "store", "detect", "coverage", "findings"},
	"detect":     {"rules", "host", "snapshot"},
	"rules":      {},
	"findings":   {"store", "keys"},
	"coverage":   {"store"},
	"policy":     {"workspace", "sched", "snapshot", "store", "findings", "coverage", "exceptions", "review"},
	"exceptions": {"coverage", "store"},
	"review":     {"workspace", "store", "findings", "exceptions"},
	"plan":       {"snapshot", "store"},
	"preview":    {"host", "refs", "plan", "extract", "snapshot", "store"},
	"export":     {"workspace", "sched", "snapshot", "host", "refs", "store", "plan", "coverage"},
	"validate":   {"workspace", "sched", "snapshot", "host", "refs", "store", "extract", "detect", "coverage", "plan", "findings"},
	"verdict":    {},
	"publish":    {"workspace", "sched", "snapshot", "refs", "store", "keys", "platform", "verdict"},
	"report":     plus(without(domain, "report"), "store"),
	"config":     {"rules", "store"},
	"registry":   {"store", "platform"},
	"envs":       {"store", "platform"},
	"pluginmgr":  {"netgate", "envs", "registry", "host", "rules", "store"},
	"models":     {"netgate", "host", "store"},
	"host":       {"platform", "keys", "store", "envs"},
	"refs":       {"platform", "keys", "store", "envs"},
	"store":      {"platform"},
	"keys":       {"platform"},
	"netgate":    {"platform"},
	"platform":   {},
	"archtest":   {},
}

// restricted maps an import path prefix to the only modules that may use it.
var restricted = map[string][]string{
	"github.com/wailsapp/wails":     {"gui"},              // MD-03: domain modules stay free of Wails
	"net/http":                      {"gui", "netgate"},   // AR-17: only netgate connects out; gui serves preview assets
	"os/exec":                       {"host", "platform"}, // only host starts plugin processes
	"syscall":                       {"platform"},         // OS APIs stay in platform (MD-03)
	"golang.org/x/sys":              {"platform"},
	"github.com/ncruces/go-sqlite3": {"store"},
	"github.com/mattn/go-sqlite3":   {"store"},
	"modernc.org/sqlite":            {"store"},
}

func repoInternal(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// imports returns the imports of the non-test Go files of each package under internal/.
func imports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	root := repoInternal(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(root, e.Name(), "*.go"))
		set := map[string]bool{}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, is := range af.Imports {
				p, _ := strconv.Unquote(is.Path.Value)
				set[p] = true
			}
		}
		out[e.Name()] = set
	}
	return out
}

func TestEveryModuleHasAPackage(t *testing.T) {
	pkgs := imports(t)
	for m := range allowed {
		if _, ok := pkgs[m]; !ok {
			t.Errorf("module %s from the module design has no package internal/%s", m, m)
		}
	}
	for p := range pkgs {
		if _, ok := allowed[p]; !ok {
			t.Errorf("internal/%s is not a module of the module design; add it there and to the allowed table", p)
		}
	}
}

func TestInternalImportsFollowTheDesign(t *testing.T) {
	for pkg, set := range imports(t) {
		ok := map[string]bool{"kernel": true}
		for _, m := range allowed[pkg] {
			ok[m] = true
		}
		var bad []string
		for imp := range set {
			if !strings.HasPrefix(imp, modPrefix) {
				continue
			}
			dep := strings.SplitN(strings.TrimPrefix(imp, modPrefix), "/", 2)[0]
			if dep != pkg && !ok[dep] {
				bad = append(bad, dep)
			}
		}
		sort.Strings(bad)
		if len(bad) > 0 {
			t.Errorf("internal/%s must not import %v (module design 2.3)", pkg, bad)
		}
	}
}

func TestRestrictedImports(t *testing.T) {
	for pkg, set := range imports(t) {
		for imp := range set {
			for prefix, owners := range restricted {
				if imp != prefix && !strings.HasPrefix(imp, prefix+"/") {
					continue
				}
				permitted := false
				for _, o := range owners {
					permitted = permitted || o == pkg
				}
				if !permitted {
					t.Errorf("internal/%s imports %s, which only %v may use", pkg, imp, owners)
				}
			}
		}
	}
}
