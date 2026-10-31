package extract

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jorben/tacet/internal/store"
)

// Purge deletes every unit row of a job, sealed or not. Body files are removed
// separately (PurgeBodies): the commit section does no file work.
func (Repository) Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrUnits
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM extract_unit WHERE attempt IN (SELECT attempt FROM extract_attempt WHERE job=?)",
		"DELETE FROM extract_attempt WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

func jobDir(root, job string) (string, error) {
	if !idPattern.MatchString(job) || !filepath.IsAbs(root) {
		return "", ErrBody
	}
	return filepath.Join(root, job), nil
}

// JobBodies is the body store of one job: its own directory under root and, per
// body, a key derived from that job's task key (derive is keys.ResourceKey over
// the task key; extract does not import keys). Erasing the task key therefore
// makes every body unreadable even before the files are removed.
func JobBodies(root, job string, derive func(job, id string) ([]byte, error)) (FileBodies, error) {
	dir, err := jobDir(root, job)
	if err != nil || derive == nil {
		return FileBodies{}, ErrBody
	}
	return FileBodies{Dir: dir, Key: func(id string) ([]byte, error) { return derive(job, id) }}, nil
}

// PurgeBodies removes a job's body directory. It is idempotent, as retention
// requires of a registered purge step.
func PurgeBodies(root string) func(ctx context.Context, job string) error {
	return func(_ context.Context, job string) error {
		dir, err := jobDir(root, job)
		if err != nil {
			return err
		}
		if err = os.RemoveAll(dir); err != nil {
			return ErrBody
		}
		return nil
	}
}
