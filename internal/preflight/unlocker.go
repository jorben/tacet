package preflight

import (
	"context"
	"encoding/json"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

// PluginUnlocker runs format.unlock on one worker: the encrypted original as a
// read reference, a fresh task-encrypted write reference for the decrypted copy,
// and the password as a host Secret (never part of the params map).
type PluginUnlocker struct {
	Session *host.Session
	Attempt kernel.AttemptRef
	Input   refs.Wire
	Output  refs.Wire
	// Seal freezes the decrypted copy after a clean stream end and returns the
	// reference the rest of the job reads. It is not called on any failure.
	Seal func() (string, error)
}

func (u *PluginUnlocker) Unlock(ctx context.Context, input string, password []byte) (string, error) {
	if u.Session == nil || u.Seal == nil || input != u.Input.Ref || u.Input.Mode != refs.Read || u.Output.Mode != refs.Write {
		return "", ErrUnlock
	}
	var done struct {
		Kind  string `json:"kind"`
		Bytes int64  `json:"bytes"`
	}
	seen := 0
	out, err := u.Session.StreamSecret(ctx, "format.unlock", u.Attempt,
		map[string]any{"input": u.Input, "output": u.Output},
		[]host.Secret{{Field: "password", Value: password}},
		func(_ context.Context, item json.RawMessage) error {
			seen++
			if seen > 1 || json.Unmarshal(item, &done) != nil || done.Kind != "unlock_done" || done.Bytes <= 0 {
				return ErrUnlock
			}
			return nil
		})
	switch {
	case err != nil:
		return "", ErrUnlock
	case out.Status == "failed" && out.Code == ErrPasswordWrong.Error():
		return "", ErrPasswordWrong
	case out.Status != "completed" || seen != 1:
		return "", ErrUnlock
	}
	ref, err := u.Seal()
	if err != nil || ref == "" {
		return "", ErrUnlock
	}
	return ref, nil
}
