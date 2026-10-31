package platform

import "errors"

var ErrFileLockBusy = errors.New("WORKSPACE_BUSY")
var ErrFileLockInvalid = errors.New("WORKSPACE_LOCK_UNAVAILABLE")
