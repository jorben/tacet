//go:build darwin

package host

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/envs"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// Importing any of these modules leaves a file beside itself.
const planted = "open(__file__ + '.imported', 'w').close()\n"

// A real plugin started from an envs launch spec must come up with the
// handshake, even though the working directory and the parent's PYTHONPATH hold
// modules named like stdlib ones that would run code if they were imported.
func TestRealPluginStartsFromAnEnvironmentLaunchSpecWithoutImportingPlantedModules(t *testing.T) {
	repoPython, err := filepath.Abs(filepath.Join("..", "..", "python"))
	if err != nil {
		t.Fatal(err)
	}
	testenv.Python(t)
	trap := t.TempDir()   // used as the working directory of the plugin
	pypath := t.TempDir() // the parent's PYTHONPATH
	for _, dir := range []string{trap, pypath} {
		for _, name := range []string{"json.py", "lxml.py", "sitecustomize.py", "usercustomize.py"} {
			if err = os.WriteFile(filepath.Join(dir, name), []byte(planted), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PYTHONPATH", pypath)
	t.Setenv("PYTHONSTARTUP", filepath.Join(trap, "json.py"))

	dbDir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dbDir, "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(envs.Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo, err := envs.NewRepository(repoPython)
	if err != nil {
		t.Fatal(err)
	}
	id := envs.Identity{OSArch: "darwin-arm64", PythonABI: "cp313",
		LockDigest: "sha256:" + strings.Repeat("a", 64), ArtifactsDigest: "sha256:" + strings.Repeat("b", 64)}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := repo.BeginDeploy(tx, id, filepath.Join(repoPython, ".venv"), testenv.VenvPython, true)
	if err != nil {
		t.Fatal(err)
	}
	// the probe is the real thing: start the plugin and complete the handshake
	probe := func(l envs.Launch) error {
		return handshake(ctx, envs.Launch{Path: l.Path, Args: append([]string{}, l.Args[:2]...), Dir: trap, Env: l.Env}, "tacet_format_docx")
	}
	state, err := repo.Finish(tx, digest, probe)
	if err != nil || state != envs.Ready {
		t.Fatalf("finish: %s %v", state, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := repo.LaunchSpec(tx, digest, "tacet_format_docx", nil, trap)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	worker, err := Start(Spec{Path: launch.Path, Args: launch.Args, Dir: launch.Dir, Env: launch.Env, Generation: "gen_envs_launch_test1"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	if err = worker.Session.Initialize(ctx, "tacet.docx", "0.1.0", 3); err != nil {
		t.Fatalf("real plugin handshake: %v", err)
	}
	for _, dir := range []string{trap, pypath} {
		if marks, _ := filepath.Glob(filepath.Join(dir, "*.imported")); len(marks) != 0 {
			t.Fatalf("planted code ran: %v", marks)
		}
	}
}

// handshake starts the module and completes initialize, then ends the process.
func handshake(ctx context.Context, l envs.Launch, module string) error {
	w, err := Start(Spec{Path: l.Path, Args: append(append([]string{}, l.Args...), module), Dir: l.Dir, Env: l.Env})
	if err != nil {
		return err
	}
	defer w.Terminate(time.Second)
	return w.Session.Initialize(ctx, "tacet.docx", "0.1.0", 1)
}
