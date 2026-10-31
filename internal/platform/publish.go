package platform

import "errors"

var ErrPublishExists = errors.New("PUBLISH_TARGET_EXISTS")
var ErrPublishUnavailable = errors.New("PUBLISH_ATOMIC_UNAVAILABLE")
var ErrPublishIO = errors.New("PUBLISH_IO_FAILED")
