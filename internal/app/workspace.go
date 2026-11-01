package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WorkspaceInfo reads the active directory, including the database and encrypted objects.
// WalkDir does not follow symlinks, so external directories never contribute to its size.
func (e *Engine) WorkspaceInfo(ctx context.Context) (WorkspaceInfo, error) {
	info := WorkspaceInfo{Dir: e.opt.Dir}
	err := filepath.WalkDir(info.Dir, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) && path != info.Dir {
			return nil // task cleanup may remove a file while the directory is being measured
		}
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		stat, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil {
			info.Bytes += stat.Size()
		}
		return err
	})
	if err != nil {
		return WorkspaceInfo{}, errors.New("WORKSPACE_UNAVAILABLE")
	}
	return info, nil
}
