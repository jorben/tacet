package platform

import "errors"

// ErrActivityUnavailable: the platform did not take the declaration; work goes on, only possibly throttled.
var ErrActivityUnavailable = errors.New("ACTIVITY_UNAVAILABLE")
