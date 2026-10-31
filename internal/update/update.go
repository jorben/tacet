package update

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jorben/tacet/internal/netgate"
)

var (
	ErrNoUpdate     = errors.New("UPDATE_NOT_AVAILABLE")
	ErrWrongTarget  = errors.New("UPDATE_WRONG_PLATFORM_OR_CHANNEL")
	ErrDowngrade    = errors.New("UPDATE_NOT_NEWER")
	ErrTooOld       = errors.New("UPDATE_FROM_VERSION_UNSUPPORTED")
	ErrDigest       = errors.New("UPDATE_PACKAGE_DIGEST_MISMATCH")
	ErrTasksPending = errors.New("UPDATE_TASKS_PENDING")
	ErrNoPackage    = errors.New("UPDATE_PACKAGE_NOT_READY")
	ErrConfig       = errors.New("UPDATE_CONFIG_INVALID")
	ErrApply        = errors.New("UPDATE_APPLY_FAILED")
)

// Tasks is what the update must settle before it is applied (SEC-15). The app wires
// it to query, jobs and retention; update only asks and instructs.
type Tasks interface {
	// Pending counts tasks awaiting review and tasks that can be resumed.
	Pending(ctx context.Context) (review, recoverable int, err error)
	// Void discards those tasks (the user's choice, never implicit).
	Void(ctx context.Context) error
	// Reexecute marks them to be run again with the new components after the update
	// (INV-03, INV-04); already published outputs are unaffected.
	Reexecute(ctx context.Context) error
}

// Quitter runs the application's exit order (module design 4.3) without update
// importing lifecycle.
type Quitter interface {
	Quit(ctx context.Context) error
}

// Installer replaces the application with a verified package. It is the
// platform's updater; update gives it a path whose digest it has checked.
type Installer interface {
	Install(ctx context.Context, packagePath string, r Release) error
}

// Choice is what the user decided about pending tasks.
type Choice string

const (
	ChoiceNone      Choice = ""
	ChoiceFinish    Choice = "finish"    // postpone the update
	ChoiceVoid      Choice = "void"      // discard the tasks
	ChoiceReexecute Choice = "reexecute" // update, then redo affected stages
)

// Pending is returned with ErrTasksPending so the interface can show the numbers.
type PendingError struct{ Review, Recoverable int }

func (e *PendingError) Error() string { return ErrTasksPending.Error() }
func (e *PendingError) Unwrap() error { return ErrTasksPending }

type Config struct {
	Dir      string // absolute; holds staging/ and ready/
	Gate     *netgate.Gate
	Verifier *Verifier
	Hosts    []string
	TLS      *tls.Config
	// URLs build the two request URLs from nothing but version, platform and channel
	// (SEC-15, INV-06): the interface they have leaves no room for anything else.
	ManifestURL func(version, platform, channel string) (manifest, signature string, err error)
	PackageURL  func(r Release) (string, error)
	Version     string // the running version
	Platform    string
	Channel     string
	Free        func(dir string) (int64, error)
	Tasks       Tasks
	Quitter     Quitter
	Installer   Installer
}

type Service struct{ cfg Config }

func New(cfg Config) (*Service, error) {
	if !filepath.IsAbs(cfg.Dir) || cfg.Gate == nil || cfg.Verifier == nil || cfg.ManifestURL == nil || cfg.PackageURL == nil || !versionRe.MatchString(cfg.Version) ||
		!platformRe.MatchString(cfg.Platform) || !channelRe.MatchString(cfg.Channel) || cfg.Free == nil || cfg.Tasks == nil || cfg.Quitter == nil || cfg.Installer == nil {
		return nil, ErrConfig
	}
	for _, d := range []string{"staging", "ready"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, d), 0o700); err != nil {
			return nil, ErrConfig
		}
	}
	return &Service{cfg: cfg}, nil
}

// vet decides whether a verified release applies to this installation.
func (s *Service) vet(r Release) error {
	switch {
	case r.Platform != s.cfg.Platform || r.Channel != s.cfg.Channel:
		return ErrWrongTarget
	case !Newer(r.Version, s.cfg.Version):
		return ErrDowngrade
	case r.MinFrom != "" && Newer(r.MinFrom, s.cfg.Version):
		return ErrTooOld
	}
	return nil
}

// parseSigned verifies first and parses second: nothing from an unsigned document
// is interpreted.
func (s *Service) parseSigned(manifest, signature []byte) (Release, error) {
	if err := s.cfg.Verifier.Verify(PurposeRelease, manifest, signature); err != nil {
		return Release{}, err
	}
	r, err := ParseRelease(manifest)
	if err != nil {
		return Release{}, err
	}
	return r, s.vet(r)
}

const maxDoc = 1 << 20

func readAll(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxDoc+1))
	if err != nil || len(b) > maxDoc {
		return nil, ErrManifest
	}
	return b, nil
}

// Check asks the update channel for the newest release under a network lease (so
// never while documents are processed). The request carries version, platform
// and channel and nothing else. ErrNoUpdate means the running version is current.
func (s *Service) Check(ctx context.Context) (Release, error) {
	murl, surl, err := s.cfg.ManifestURL(s.cfg.Version, s.cfg.Platform, s.cfg.Channel)
	if err != nil {
		return Release{}, ErrConfig
	}
	lease, err := s.cfg.Gate.Acquire(netgate.Network)
	if err != nil {
		return Release{}, err
	}
	defer lease.Release()
	fx, err := lease.Fetcher(netgate.Options{Hosts: s.cfg.Hosts, TLS: s.cfg.TLS})
	if err != nil {
		return Release{}, err
	}
	get := func(u string) ([]byte, error) {
		rep, err := fx.Do(ctx, netgate.Get{URL: u})
		if err != nil {
			return nil, err
		}
		defer rep.Body.Close()
		return readAll(rep.Body)
	}
	manifest, err := get(murl)
	if err != nil {
		return Release{}, err
	}
	sig, err := get(surl)
	if err != nil {
		return Release{}, err
	}
	r, err := s.parseSigned(manifest, sig)
	if errors.Is(err, ErrDowngrade) {
		return Release{}, ErrNoUpdate
	}
	return r, err
}

func (s *Service) staging(r Release) string {
	return filepath.Join(s.cfg.Dir, "staging", r.Version+"-"+r.SHA256[:16])
}
func (s *Service) ready(r Release) string {
	return filepath.Join(s.cfg.Dir, "ready", r.Version+"-"+r.SHA256[:16])
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

// register moves a package into ready/ only if it is exactly the one the signed
// release describes.
func (s *Service) register(r Release, path string) error {
	sum, size, err := hashFile(path)
	if err != nil || size != r.Size || sum != r.SHA256 {
		os.Remove(path)
		return ErrDigest
	}
	dir := s.ready(r)
	if err := os.RemoveAll(dir); err != nil || os.MkdirAll(dir, 0o700) != nil {
		return ErrApply
	}
	if err := os.Rename(path, filepath.Join(dir, r.Name)); err != nil {
		return ErrApply
	}
	return nil
}

// Download fetches the package of a verified release and registers it as ready.
// The transfer resumes only a partial file of the same release.
func (s *Service) Download(ctx context.Context, r Release) error {
	if err := s.vet(r); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.ready(r), r.Name)); err == nil {
		return nil
	}
	url, err := s.cfg.PackageURL(r)
	if err != nil {
		return ErrConfig
	}
	stage := s.staging(r)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return ErrApply
	}
	part := filepath.Join(stage, r.Name+".part")
	var have int64
	if info, err := os.Stat(part); err == nil {
		have = info.Size()
	}
	if have > r.Size {
		os.Remove(part)
		have = 0
	}
	if free, err := s.cfg.Free(s.cfg.Dir); err != nil || free < r.Size-have+r.Size/10 {
		return ErrApply
	}
	lease, err := s.cfg.Gate.Acquire(netgate.Network)
	if err != nil {
		return err
	}
	defer lease.Release()
	fx, err := lease.Fetcher(netgate.Options{Hosts: s.cfg.Hosts, TLS: s.cfg.TLS})
	if err != nil {
		return err
	}
	// resuming needs the entity of the partial file; without it the transfer starts over
	rep, err := fx.Do(ctx, netgate.Get{URL: url})
	if err != nil {
		return err
	}
	defer rep.Body.Close()
	if rep.Total != 0 && rep.Total != r.Size {
		return ErrDigest
	}
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ErrApply
	}
	n, copyErr := io.Copy(out, io.LimitReader(rep.Body, r.Size+1))
	if cerr := out.Close(); copyErr == nil {
		copyErr = cerr
	}
	switch {
	case n > r.Size:
		os.Remove(part)
		return ErrDigest
	case copyErr != nil:
		return copyErr
	case n < r.Size:
		os.Remove(part) // an incomplete package is not worth keeping
		return io.ErrUnexpectedEOF
	}
	return s.register(r, part)
}

// ImportOffline takes a package, its release description and signature from local
// files; it goes through exactly the checks a downloaded one does.
func (s *Service) ImportOffline(manifest, signature []byte, packagePath string) (Release, error) {
	r, err := s.parseSigned(manifest, signature)
	if err != nil {
		return Release{}, err
	}
	src, err := os.Open(packagePath)
	if err != nil {
		return Release{}, ErrNoPackage
	}
	defer src.Close()
	stage := s.staging(r)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return Release{}, ErrApply
	}
	part := filepath.Join(stage, r.Name+".part")
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return Release{}, ErrApply
	}
	n, copyErr := io.Copy(out, io.LimitReader(src, r.Size+1))
	if cerr := out.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil || n != r.Size {
		os.Remove(part)
		return Release{}, ErrDigest
	}
	return r, s.register(r, part)
}

// Apply settles the pending tasks as the user chose, takes the maintenance lease
// (which also keeps document processing out), runs the exit order and hands the
// verified package to the installer. Pending tasks without a choice, or a choice of
// "finish", stop it before anything changes.
func (s *Service) Apply(ctx context.Context, r Release, choice Choice) error {
	if err := s.vet(r); err != nil {
		return err
	}
	pkg := filepath.Join(s.ready(r), r.Name)
	if sum, size, err := hashFile(pkg); err != nil || size != r.Size || sum != r.SHA256 {
		return ErrNoPackage // missing, or changed on disk since it was verified
	}
	review, recoverable, err := s.cfg.Tasks.Pending(ctx)
	if err != nil {
		return ErrApply
	}
	if review+recoverable > 0 {
		switch choice {
		case ChoiceVoid, ChoiceReexecute:
		default:
			return &PendingError{review, recoverable}
		}
	}
	lease, err := s.cfg.Gate.Acquire(netgate.Maintenance)
	if err != nil {
		return err
	}
	defer lease.Release()
	if review+recoverable > 0 {
		var err error
		if choice == ChoiceVoid {
			err = s.cfg.Tasks.Void(ctx)
		} else {
			err = s.cfg.Tasks.Reexecute(ctx)
		}
		if err != nil {
			return ErrApply
		}
	}
	if err := s.cfg.Quitter.Quit(ctx); err != nil {
		return ErrApply
	}
	if err := s.cfg.Installer.Install(ctx, pkg, r); err != nil {
		return ErrApply
	}
	return nil
}

// Redeploy marks third-party environments built for another Python ABI or platform
// as needing redeployment (envs.Repository.MarkRedeploy bound to a transaction by the
// wiring; update may not use the store itself).
type Redeploy func(osArch, abi string) ([]string, error)

// FirstRun is called by the new version at its first start, inside the migration
// commit: plugins whose environment no longer fits stay disabled until redeployed
// (architecture 7.3). Built-in environments ship with the app and are not touched.
func FirstRun(mark Redeploy, osArch, abi string) ([]string, error) {
	if mark == nil || osArch == "" || abi == "" {
		return nil, ErrConfig
	}
	return mark(osArch, abi)
}
