//go:build !darwin && !windows

package platform

func DeclareActivity(bool) error { return ErrActivityUnavailable }

func activityDeclared() bool { return false }
