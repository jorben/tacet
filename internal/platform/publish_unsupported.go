//go:build !darwin && !windows

package platform

func PublishExclusive(temporary, destination string) error { return ErrPublishUnavailable }
