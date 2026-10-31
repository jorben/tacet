//go:build !darwin && !windows

package platform

import "os/exec"

type treeImpl struct{}

func (treeImpl) pid() int       { return 0 }
func (treeImpl) kill() error    { return ErrProcessTree }
func (treeImpl) release() error { return nil }

// StartTree fails explicitly outside the MVP platform baseline; it never
// starts an unmanaged process.
func StartTree(cmd *exec.Cmd) (*Tree, error) { return nil, ErrProcessTree }
