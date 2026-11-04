package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jorben/tacet/internal/netgate"
)

var errVerify = errors.New("verify")

// Pause stops a running download at once and keeps what has been received. It is
// also what leaving the program does (PauseAll).
func (s *Service) Pause(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.active[key]
	if !ok {
		return ErrNotDownloadable
	}
	j.paused = true
	j.cancel()
	return nil
}

func (s *Service) PauseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.active {
		j.paused = true
		j.cancel()
	}
}

func (s *Service) begin(key string, cancel context.CancelFunc) (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.active[key]; busy {
		return nil, ErrBusy
	}
	j := &job{cancel: cancel}
	s.active[key] = j
	return j, nil
}

func (s *Service) end(key string) {
	s.mu.Lock()
	delete(s.active, key)
	s.mu.Unlock()
}

// Download runs the whole preparation of a model that is pending, paused or in
// error: transfer (under a network lease, so never while documents are processed),
// verification of every file, atomic registration, and the session preparation. It
// returns when the model is ready, paused (ErrPaused) or failed (the error state
// carries the code). The network is only ever used because the caller asked.
// In error with installed files it is the "download again" the screens offer for files
// that changed and after a failed preparation (requirements 3.4.1): a copy that still
// matches is loaded again without the network, one that does not is replaced once the new
// files verified (finish).
func (s *Service) Download(ctx context.Context, key string) error {
	e, err := s.entry(key)
	if err != nil {
		return err
	}
	st, _, err := s.stored(ctx, e)
	if err != nil {
		return ErrFailed
	}
	switch st {
	case StatePending, StatePaused, StateError, StateDownloading, StateVerifying:
	default:
		return ErrNotDownloadable
	}
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	j, err := s.begin(key, cancel)
	if err != nil {
		return err
	}
	defer s.end(key)

	if st == StateError {
		// finish would only throw the same bytes away, and a failed transfer would turn a usable
		// copy into a network error.
		if _, err := s.checkFiles(e, false); err == nil {
			return s.prepare(ctx, e)
		}
	}
	if err := s.transfer(dctx, e); err != nil {
		return s.settle(ctx, e, j, err)
	}
	if err := s.finish(ctx, e); err != nil {
		return err
	}
	return s.prepare(ctx, e)
}

// settle records how a transfer ended.
func (s *Service) settle(ctx context.Context, e Entry, j *job, err error) error {
	s.mu.Lock()
	paused := j.paused
	s.mu.Unlock()
	switch {
	case paused || ctx.Err() != nil:
		s.set(ctx, e, StatePaused, "")
		if paused {
			return ErrPaused
		}
		return ctx.Err()
	case errors.Is(err, errVerify):
		os.RemoveAll(s.staging(e)) // untrusted half-products are not kept
		s.setEntity(ctx, e, "", "")
		s.set(ctx, e, StateError, CodeVerify)
	case errors.Is(err, errDisk):
		s.set(ctx, e, StateError, CodeDisk)
	default:
		var busy *netgate.BusyError
		if errors.As(err, &busy) {
			return err // processing is running: nothing was started, the state is as it was
		}
		s.set(ctx, e, StateError, CodeNetwork)
	}
	return err
}

var errDisk = errors.New("disk")

func (s *Service) transfer(ctx context.Context, e Entry) error {
	dir := s.staging(e)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errDisk
	}
	need := e.Total() - dirSize(dir)
	need += e.Total() / 10
	if free, err := s.cfg.Free(s.cfg.Root); err != nil || free < need {
		return errDisk
	}
	lease, err := s.cfg.Gate.Acquire(netgate.Network)
	if err != nil {
		return err
	}
	defer lease.Release()
	fetcher, err := lease.Fetcher(netgate.Options{Hosts: s.cfg.Hosts, TLS: s.cfg.TLS})
	if err != nil {
		return err
	}
	if err := s.set(ctx, e, StateDownloading, ""); err != nil {
		return ErrFailed
	}
	for _, f := range e.Files {
		if err := s.fetchFile(ctx, fetcher, e, f, dir); err != nil {
			return err
		}
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// fetchFile brings one file to its final name in staging. A finished file is only
// ever present under its real name after its hash has matched.
func (s *Service) fetchFile(ctx context.Context, fx *netgate.Fetcher, e Entry, f File, dir string) error {
	final, part := filepath.Join(dir, f.Name), filepath.Join(dir, f.Name+".part")
	if info, err := os.Stat(final); err == nil && info.Size() == f.Size {
		if sum, _, err := hashFile(final); err == nil && sum == f.SHA256 {
			return nil
		}
		os.Remove(final)
	}
	url, err := s.cfg.URL(e, f.Name)
	if err != nil {
		return netgate.ErrNetworkTarget
	}
	var offset int64
	if info, err := os.Stat(part); err == nil {
		offset = info.Size()
	}
	file, entity := s.entity(ctx, e)
	if offset > f.Size || (offset > 0 && (file != f.Name || entity == "")) {
		os.Remove(part) // a resume point must be bound to this file and a known entity
		offset = 0
	}
	reply, err := fx.Do(ctx, netgate.Get{URL: url, Offset: offset, Entity: entity})
	if err != nil {
		return err
	}
	defer reply.Body.Close()
	if reply.Total != 0 && reply.Total != f.Size {
		return errVerify // the server holds another file than the catalog describes
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if reply.Offset == 0 {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC // the server restarted the transfer
	}
	if err := s.setEntity(ctx, e, f.Name, reply.Entity); err != nil {
		return ErrFailed
	}
	out, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return errDisk
	}
	have := reply.Offset
	n, copyErr := io.Copy(out, io.LimitReader(reply.Body, f.Size-have+1))
	have += n
	if cerr := out.Close(); copyErr == nil {
		copyErr = cerr
	}
	switch {
	case have > f.Size:
		return errVerify
	case have < f.Size:
		if copyErr == nil {
			copyErr = io.ErrUnexpectedEOF
		}
		return copyErr // cut off: the partial file stays and is resumed
	case copyErr != nil:
		return copyErr
	}
	sum, size, err := hashFile(part)
	if err != nil || size != f.Size || sum != f.SHA256 {
		return errVerify
	}
	if err := os.Rename(part, final); err != nil {
		return errDisk
	}
	return nil
}

// stamp is a cheap fingerprint of a directory's files (name, size, modification time). It
// decides whether CheckBinding hashes the files again. A rewrite that keeps the size and
// falls in the same tick of the file clock keeps the stamp too (1 ms measured on Linux, where
// tmpfs is fast enough to hit it, #29), and a file the manifest does not list does not change
// it, so loading never relies on it. A listed file that is not a regular file is errVerify,
// never a stamp: an empty cache must not match it.
func stamp(e Entry, dir string) (string, error) {
	h := sha256.New()
	for _, f := range e.Files {
		info, err := os.Lstat(filepath.Join(dir, f.Name))
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errVerify
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", f.Name, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyDir demands exactly the manifest's files, with their sizes and hashes. A
// file that is not in the manifest is a failure too: nothing unlisted is registered.
func verifyDir(e Entry, dir string) error {
	es, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, f := range e.Files {
		want[f.Name] = true
	}
	for _, x := range es {
		if !want[x.Name()] {
			return errVerify
		}
	}
	for _, f := range e.Files {
		path := filepath.Join(dir, f.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
			return errVerify
		}
		if sum, _, err := hashFile(path); err != nil || sum != f.SHA256 {
			return errVerify
		}
	}
	return nil
}

// finish verifies the staged package as a whole and registers it atomically. A
// failure leaves any usable installed copy alone.
func (s *Service) finish(ctx context.Context, e Entry) error {
	if err := s.set(ctx, e, StateVerifying, ""); err != nil {
		return ErrFailed
	}
	staging, installed := s.staging(e), s.installed(e)
	if err := verifyDir(e, staging); err != nil {
		os.RemoveAll(staging)
		s.setEntity(ctx, e, "", "")
		s.set(ctx, e, StateError, CodeVerify)
		return errVerify
	}
	if verifyDir(e, installed) == nil {
		os.RemoveAll(staging) // the same package is already registered and intact
	} else {
		old := installed + ".old"
		os.RemoveAll(old)
		if _, err := os.Stat(installed); err == nil {
			if err := os.Rename(installed, old); err != nil {
				s.set(ctx, e, StateError, CodeInternal)
				return ErrFailed
			}
		}
		if err := os.Rename(staging, installed); err != nil {
			os.Rename(old, installed)
			s.set(ctx, e, StateError, CodeInternal)
			return ErrFailed
		}
		os.RemoveAll(old)
	}
	s.setEntity(ctx, e, "", "")
	if st, err := stamp(e, installed); err == nil {
		s.mu.Lock()
		s.verified[e.Key()] = st
		s.mu.Unlock()
	}
	if err := s.set(ctx, e, StateInstalled, ""); err != nil {
		return ErrFailed
	}
	return nil
}

// Prepare loads the installed model and runs the fixed sample in a worker; only then
// is it ready for this session. Files that no longer match the manifest are not
// loaded: the model goes to the error state and the user is offered a fresh download.
func (s *Service) Prepare(ctx context.Context, key string) error {
	e, err := s.entry(key)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := s.begin(key, cancel); err != nil {
		return err
	}
	defer s.end(key)
	return s.prepare(pctx, e)
}

// prepare runs with the model's operation slot already held by the caller.
func (s *Service) prepare(ctx context.Context, e Entry) error {
	key, pctx := e.Key(), ctx
	st, _, err := s.stored(ctx, e)
	if err != nil {
		return ErrFailed
	}
	switch st {
	case StateInstalled, StatePreparing, StateReady, StateError:
	default:
		return ErrNotInstalled
	}
	if _, err := os.Stat(s.installed(e)); err != nil {
		return ErrNotInstalled
	}
	if err := s.set(ctx, e, StatePreparing, ""); err != nil {
		return ErrFailed
	}
	if _, err := s.checkFiles(e, true); err != nil { // the files are loaded: hashed in full
		s.set(ctx, e, StateError, CodeChanged)
		return err
	}
	if err := s.cfg.Prepare(pctx, s.installed(e), e); err != nil {
		s.mu.Lock()
		delete(s.ready, key)
		s.mu.Unlock()
		if ctx.Err() != nil {
			s.set(ctx, e, StateInstalled, "")
			return ctx.Err()
		}
		s.set(ctx, e, StateError, CodePrepare)
		return ErrFailed
	}
	s.mu.Lock()
	s.ready[key] = true
	s.mu.Unlock()
	return s.set(ctx, e, StateReady, "")
}

// SessionLost records that the worker which had the model loaded is gone: the model
// stays installed but must be loaded and checked again; nothing is downloaded.
func (s *Service) SessionLost(ctx context.Context, key string) error {
	e, err := s.entry(key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	was := s.ready[key]
	delete(s.ready, key)
	s.mu.Unlock()
	if !was {
		return nil
	}
	st, _, err := s.stored(ctx, e)
	if err != nil {
		return ErrFailed
	}
	if st != StateReady {
		return nil // an error, a pause or a running operation says more than "installed"
	}
	return s.set(ctx, e, StateInstalled, "")
}

// Discard clears the partial download (the first step of "download again"); transfer
// data is bound to one version and entity and is never reused after this. It says nothing
// about installed files: a model in error stays in error until a download or a preparation
// has checked them, so damaged files are never shown as installed.
func (s *Service) Discard(ctx context.Context, key string) error {
	e, err := s.entry(key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, busy := s.active[key]
	s.mu.Unlock()
	if busy {
		return ErrBusy
	}
	if err := os.RemoveAll(s.staging(e)); err != nil {
		return ErrFailed
	}
	s.setEntity(ctx, e, "", "")
	if _, err := os.Stat(s.installed(e)); err == nil {
		st, _, err := s.stored(ctx, e)
		if err != nil {
			return ErrFailed
		}
		if st == StateError {
			return nil
		}
		return s.set(ctx, e, StateInstalled, "")
	}
	return s.set(ctx, e, StatePending, "")
}

// checkFiles verifies the installed files against the manifest. With full (loading,
// architecture 8.3) they are hashed every time; otherwise only when the fingerprint
// changed since they were last hashed in full, which misses a rewrite that keeps size and
// modification time (stamp).
func (s *Service) checkFiles(e Entry, full bool) (string, error) {
	dir := s.installed(e)
	st, err := stamp(e, dir)
	if err != nil && !errors.Is(err, errVerify) {
		return "", ErrBindingMissing
	}
	s.mu.Lock()
	known := s.verified[e.Key()]
	s.mu.Unlock()
	if err == nil && !full && known == st {
		return dir, nil
	}
	// verifyDir also refuses the file stamp found not to be a regular one (errVerify)
	if verifyDir(e, dir) != nil {
		s.mu.Lock()
		delete(s.verified, e.Key()) // a refusal is never undone by a stamp from before it
		s.mu.Unlock()
		return "", ErrBindingMismatch
	}
	s.mu.Lock()
	s.verified[e.Key()] = st
	s.mu.Unlock()
	return dir, nil
}

// Binding is a model a task may be bound to.
type Binding struct {
	Entry Entry
	Dir   string
	Ready bool // loaded and checked in this session
}

// CheckBinding is called when a task starts or resumes and when a plan is switched
// (INV-03, INV-06): the model the task froze by ID and digest must be installed with
// files that still match. Missing or different means blocked; the caller never falls
// back to rules only or to another model.
func (s *Service) CheckBinding(ctx context.Context, id, digest string) (Binding, error) {
	for _, e := range s.entries {
		if e.ID != id || e.Digest != digest {
			continue
		}
		st, code, err := s.stored(ctx, e)
		if err != nil {
			return Binding{}, ErrFailed
		}
		// A failed preparation leaves the registered files intact: the binding is about
		// the files, readiness (Binding.Ready) is reported separately.
		if st != StateInstalled && st != StateReady && st != StatePreparing && !(st == StateError && code == CodePrepare) {
			return Binding{}, ErrBindingMissing
		}
		dir, err := s.checkFiles(e, false) // called often; the load hashes in full
		if err != nil {
			if errors.Is(err, ErrBindingMismatch) {
				s.set(ctx, e, StateError, CodeChanged)
			}
			return Binding{}, err
		}
		s.mu.Lock()
		ready := s.ready[e.Key()]
		s.mu.Unlock()
		return Binding{Entry: e, Dir: dir, Ready: ready}, nil
	}
	return Binding{}, ErrBindingMissing
}
