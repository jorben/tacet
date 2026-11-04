package gui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/app"
)

// fakeCore answers what the service forwards; unset methods panic through the nil interface.
type fakeCore struct {
	app.Core
	mu        sync.Mutex
	status    app.BatchStatus
	imported  app.ImportRequest
	started   app.StartRequest
	retried   []string
	pw        []byte
	err       error
	notify    func(string)
	closed    bool
	workspace app.WorkspaceInfo
	modelDirs map[string]string
	asked     []string
}

func (f *fakeCore) Import(_ context.Context, r app.ImportRequest) (app.BatchStatus, error) {
	f.imported = r
	return app.BatchStatus{ID: "b0001"}, f.err
}
func (f *fakeCore) Start(_ context.Context, r app.StartRequest) error { f.started = r; return f.err }
func (f *fakeCore) Status(context.Context, string, int) (app.BatchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}
func (f *fakeCore) Retry(_ context.Context, _ string, j []string) error { f.retried = j; return f.err }
func (f *fakeCore) Unlock(_ context.Context, _ string, pw []byte) error {
	f.pw = append([]byte(nil), pw...)
	return f.err
}
func (f *fakeCore) Close() error { f.closed = true; return nil }
func (f *fakeCore) WorkspaceInfo(context.Context) (app.WorkspaceInfo, error) {
	return f.workspace, f.err
}
func (f *fakeCore) ModelDirectory(_ context.Context, id string) (string, error) {
	f.asked = append(f.asked, id)
	if d, ok := f.modelDirs[id]; ok {
		return d, f.err
	}
	return "", errors.New("MODEL_NOT_INSTALLED")
}
func (f *fakeCore) DownloadModel(_ context.Context, id string) error {
	f.asked = append(f.asked, "download:"+id)
	return f.err
}
func (f *fakeCore) Subscribe(fn func(string)) func() {
	f.notify = fn
	return func() { f.notify = nil }
}

type fakeHost struct {
	saved, picked string
	opened        string
	cancel        bool
	err           error
	mu            sync.Mutex
	events        []string
	last          any
}

func (h *fakeHost) PickFiles(string) ([]string, error) { return []string{"/a.txt"}, nil }
func (h *fakeHost) SaveText(_, name, text string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saved = name + "|" + text
	return !h.cancel, h.err
}
func (h *fakeHost) PickText(string, int64) (string, bool, error) {
	return h.picked, !h.cancel && h.picked != "", h.err
}
func (h *fakeHost) PickFolder(string) (string, error) { return "/out", nil }
func (h *fakeHost) OpenDirectory(path string) error   { h.opened = path; return h.err }
func (h *fakeHost) Emit(e string, d any) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.last = d
	h.mu.Unlock()
}
func (h *fakeHost) count() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.events) }

func opener(c app.Core, err error) Opener {
	return func(context.Context) (app.Core, error) { return c, err }
}

func TestWorkspaceOpensTheActiveDirectory(t *testing.T) {
	c := &fakeCore{workspace: app.WorkspaceInfo{Dir: "/private/custom-workspace", Bytes: 1234}}
	h := &fakeHost{}
	s := NewService(opener(c, nil), h)
	info, err := s.WorkspaceInfo()
	if err != nil || info != c.workspace {
		t.Fatalf("%+v %v", info, err)
	}
	if err := s.OpenWorkspace(); err != nil || h.opened != c.workspace.Dir {
		t.Fatalf("%q %v", h.opened, err)
	}
	h.err = errors.New("open /private/secret: denied")
	if err := s.OpenWorkspace(); err == nil || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("%v", err)
	}
	c.err = errors.New("WORKSPACE_UNAVAILABLE")
	h.opened = ""
	if err := s.OpenWorkspace(); err == nil || h.opened != "" {
		t.Fatalf("must not open on query failure: %q %v", h.opened, err)
	}
	if err := NewService(nil, nil).OpenWorkspace(); err == nil {
		t.Fatal("missing host must fail")
	}
}

func TestCloseHandlerReady(t *testing.T) {
	s := NewService(nil, nil)
	if s.closeReady.Load() {
		t.Fatal("close must keep its native behaviour until the frontend is ready")
	}
	s.CloseHandlerReady()
	if !s.closeReady.Load() {
		t.Fatal("CloseHandlerReady must mark the frontend as ready")
	}
}

func TestAppInfoReportsEngineState(t *testing.T) {
	info := NewService(nil, nil).AppInfo()
	if info.Name == "" || info.Version == "" || info.Ready || info.Problem != "ENGINE_UNAVAILABLE" {
		t.Fatalf("%+v", info)
	}
	info = NewService(opener(nil, errors.New("WORKSPACE_BUSY")), nil).AppInfo()
	if info.Ready || info.Problem != "WORKSPACE_BUSY" {
		t.Fatalf("a busy workspace must be named: %+v", info)
	}
	info = NewService(opener(&fakeCore{}, nil), nil).AppInfo()
	if !info.Ready || info.Problem != "" {
		t.Fatalf("%+v", info)
	}
}

func TestCommandsReachTheCoreAndHideDetails(t *testing.T) {
	c := &fakeCore{}
	s := NewService(opener(c, nil), &fakeHost{})
	if _, err := s.ImportFiles(ImportArgs{Files: []string{"/a.txt"}, Quick: true, OutputDir: "/out", Preset: "p"}); err != nil {
		t.Fatal(err)
	}
	if c.imported.Kind != "quick" || c.imported.OutputDir != "/out" || c.imported.Preset != "p" || len(c.imported.Files) != 1 {
		t.Fatalf("%+v", c.imported)
	}
	if c.imported.ParkWaiting {
		t.Fatal("parked without being asked to")
	}
	if _, err := s.ImportFiles(ImportArgs{Files: []string{"/a.txt"}, OutputDir: "/out", ParkWaiting: true}); err != nil {
		t.Fatal(err)
	}
	if !c.imported.ParkWaiting {
		t.Fatal("the request to park did not reach the core")
	}
	if err := s.Start(StartArgs{Batch: "b0001", Authorize: true, AllowPartial: true}); err != nil || !c.started.Authorize || !c.started.AllowPartial {
		t.Fatalf("%+v %v", c.started, err)
	}
	s.RetryFiles("b0001", []string{"j1"})
	if len(c.retried) != 1 {
		t.Fatal("retry scope lost")
	}
	// a path inside an error never reaches the frontend
	c.err = errors.New("open /Users/secret/客户名单.docx: permission denied")
	_, err := s.ImportFiles(ImportArgs{Files: []string{"/a.txt"}, OutputDir: "/out"})
	if err == nil || strings.Contains(err.Error(), "secret") || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("leaked or unnamed: %v", err)
	}
	c.err = errors.New("QUICK_TASK_ACTIVE")
	if err = s.Start(StartArgs{}); err == nil || err.Error() != "QUICK_TASK_ACTIVE" {
		t.Fatalf("a closed code must pass through: %v", err)
	}
	c.err = errors.New("NOT_ALLOWED: the file /x is not waiting")
	if err = s.Start(StartArgs{}); err.Error() != "NOT_ALLOWED" {
		t.Fatalf("detail after the code must be dropped: %v", err)
	}
}

func TestUnavailableEngineRefusesEveryCommand(t *testing.T) {
	s := NewService(opener(nil, errors.New("KEYSTORE_UNAVAILABLE")), &fakeHost{})
	if _, err := s.ImportFiles(ImportArgs{Files: []string{"/a"}, OutputDir: "/o"}); err == nil || err.Error() != "KEYSTORE_UNAVAILABLE" {
		t.Fatalf("%v", err)
	}
	if err := s.PauseBatch("b"); err == nil {
		t.Fatal("pause without an engine must fail")
	}
	if err := s.UnlockFile("j", "x"); err == nil {
		t.Fatal("unlock without an engine must fail")
	}
}

func TestUnlockPasswordIsForwardedOnly(t *testing.T) {
	c := &fakeCore{}
	s := NewService(opener(c, nil), &fakeHost{})
	if err := s.UnlockFile("j1", "correct horse"); err != nil || string(c.pw) != "correct horse" {
		t.Fatalf("%q %v", c.pw, err)
	}
}

func TestChangesAreCoalescedIntoOneStatusEvent(t *testing.T) {
	h := &fakeHost{}
	c := &fakeCore{status: app.BatchStatus{ID: "b0001", Phase: "running"}}
	s := NewService(opener(c, nil), h)
	if !s.AppInfo().Ready || c.notify == nil {
		t.Fatal("the service must subscribe to the core")
	}
	for i := 0; i < 50; i++ {
		c.notify("b0001")
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	if n := h.count(); n != 1 {
		t.Fatalf("50 changes produced %d events, want 1", n)
	}
	if st, ok := h.last.(app.BatchStatus); !ok || st.ID != "b0001" || st.Phase != "running" {
		t.Fatalf("%+v", h.last)
	}
	c.notify("b0001") // a later change is reported again
	for deadline = time.Now().Add(3 * time.Second); h.count() < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if h.count() != 2 {
		t.Fatalf("events %d", h.count())
	}
}

func TestShutdownClosesTheCoreAndStopsEvents(t *testing.T) {
	c := &fakeCore{}
	s := NewService(opener(c, nil), &fakeHost{})
	s.AppInfo()
	if err := Shutdown(s); err != nil || !c.closed || c.notify != nil {
		t.Fatalf("closed=%v notify=%v err=%v", c.closed, c.notify != nil, err)
	}
	if err := Shutdown(s); err != nil {
		t.Fatal("a second shutdown must be harmless")
	}
}

func TestCodeOf(t *testing.T) {
	for in, want := range map[string]string{"WORKSPACE_BUSY": "WORKSPACE_BUSY", "A_1: detail": "A_1", "lower": "INTERNAL_ERROR", "has space": "INTERNAL_ERROR", "": "INTERNAL_ERROR", "/p/a": "INTERNAL_ERROR"} {
		if got := codeOf(errors.New(in)); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

type previewCore struct {
	fakeCore
	png map[string][]byte
	any bool // answers every token: the handler's own check of the token's shape is then what refuses
}

func (p *previewCore) PreviewImage(t string) ([]byte, bool) {
	if p.any {
		return []byte("page"), true
	}
	b, ok := p.png[t]
	return b, ok
}
func (p *previewCore) PagePreview(_ context.Context, job string, page, dpi int) (app.PagePreview, error) {
	if p.err != nil {
		return app.PagePreview{}, p.err
	}
	return app.PagePreview{Job: job, Page: page, Image: "0123456789abcdef01234567"}, nil
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestPreviewPagesAreServedDecryptedWithoutCachingAndOnlyByToken(t *testing.T) {
	core := &previewCore{png: map[string][]byte{"0123456789abcdef01234567": []byte("\x89PNG-bytes")}}
	s := NewService(opener(core, nil), &fakeHost{})
	s.AppInfo()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app asset")) })
	h := AssetHandler(s, next)
	if rec := serve(h, "GET", "/index.html"); rec.Body.String() != "app asset" {
		t.Fatalf("other paths must reach the app: %q", rec.Body.String())
	}
	rec := serve(h, "GET", PreviewPath+"0123456789abcdef01234567.png")
	if rec.Code != 200 || rec.Body.String() != "\x89PNG-bytes" || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	for _, p := range []string{"0123456789abcdef01234568.png", "short.png", "../../etc/passwd", "0123456789ABCDEF01234567.png", "", "0123456789abcdef0123456.png", "0123456789abcdef012345678.png", "0123456789abcdef0123456g.png"} {
		rec = serve(h, "GET", PreviewPath+p)
		if rec.Code != 404 || rec.Header().Get("Cache-Control") != "no-store" || rec.Body.Len() > 1 {
			t.Errorf("%q: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	core.any = true
	for _, p := range []string{"0123456789abcdef0123456.png", "0123456789abcdef012345678.png", "0123456789abcdef0123456g.png", "0123456789ABCDEF01234567.png", "../x.png"} {
		if rec = serve(h, "GET", PreviewPath+p); rec.Code != 404 {
			t.Errorf("a token of the wrong shape (%s) must not even be looked up: %d", p, rec.Code)
		}
	}
	if rec = serve(h, "GET", PreviewPath+"ffffffffffffffffffffffff.png"); rec.Code != 200 {
		t.Fatalf("a well-formed token: %d", rec.Code)
	}
	if rec = serve(h, "POST", PreviewPath+"0123456789abcdef01234567.png"); rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
}

func TestPreviewServiceWithoutAnEngineOrWithAnEngineThatCannotServeImages(t *testing.T) {
	s := NewService(nil, nil)
	h := AssetHandler(s, http.NotFoundHandler())
	if rec := serve(h, "GET", PreviewPath+"0123456789abcdef01234567.png"); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
	if _, err := s.PagePreview("j", 1, 100); err == nil || err.Error() != "ENGINE_UNAVAILABLE" {
		t.Fatalf("%v", err)
	}
	c := &previewCore{}
	c.err = errors.New("open /Users/x/y.pdf: no")
	s2 := NewService(opener(c, nil), nil)
	if _, err := s2.PagePreview("j", 1, 100); err == nil || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("paths must not leak: %v", err)
	}
	c.err = nil
	if pp, err := s2.PagePreview("j9", 2, 100); err != nil || pp.Job != "j9" || pp.Page != 2 {
		t.Fatalf("%+v %v", pp, err)
	}
}

func TestConfigFilesGoThroughTheHostAndStayBounded(t *testing.T) {
	h := &fakeHost{picked: `{"tacet_config":1}`}
	s := NewService(nil, h)
	if text, err := s.OpenConfigFile(); err != nil || text != h.picked {
		t.Fatalf("%q %v", text, err)
	}
	h.cancel = true
	if text, err := s.OpenConfigFile(); err != nil || text != "" {
		t.Fatalf("cancel is not an error: %q %v", text, err)
	}
	h.cancel = false
	if ok, err := s.SaveConfigFile("a.json", "{}"); err != nil || !ok || h.saved != "a.json|{}" {
		t.Fatalf("%v %v %q", ok, err, h.saved)
	}
	h.cancel = true
	if ok, err := s.SaveConfigFile("a.json", "{}"); err != nil || ok {
		t.Fatalf("cancelled save: %v %v", ok, err)
	}
	h.cancel = false
	for _, text := range []string{"", strings.Repeat("x", MaxExchangeFile+1)} {
		if _, err := s.SaveConfigFile("a.json", text); err == nil || err.Error() != "INVALID_REQUEST" {
			t.Fatalf("size %d: %v", len(text), err)
		}
	}
	h.err = errors.New("open /Users/x/secret.json: denied")
	if _, err := s.OpenConfigFile(); err == nil || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("paths must not leak: %v", err)
	}
	if _, err := NewService(nil, nil).OpenConfigFile(); err == nil {
		t.Fatal("no host")
	}
}

func TestSettingsAreForwardedAndErrorsAreClosedCodes(t *testing.T) {
	c := &settingsCore{}
	s := NewService(opener(c, nil), &fakeHost{})
	got, err := s.SaveSettings(app.Settings{Theme: "dark", RetentionDays: 30})
	if err != nil || got.Theme != "dark" || c.saved.RetentionDays != 30 {
		t.Fatalf("%+v %v", got, err)
	}
	if st, err := s.Settings(); err != nil || st.Theme != "dark" {
		t.Fatalf("%+v %v", st, err)
	}
	c.err = errors.New("open /Users/x/y: no")
	if _, err = s.SaveSettings(app.Settings{}); err == nil || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("%v", err)
	}
}

type settingsCore struct {
	fakeCore
	saved app.Settings
}

func (c *settingsCore) SettingsGet(context.Context) (app.Settings, error) { return c.saved, c.err }
func (c *settingsCore) SettingsSave(_ context.Context, in app.Settings) (app.Settings, error) {
	if c.err != nil {
		return app.Settings{}, c.err
	}
	c.saved = in
	return in, nil
}

func TestModelDirectoryOpensTheFolderTheEngineReports(t *testing.T) {
	dir := "/Users/x/Library/Application Support/Tacet/models/installed/astrlink-guard_0.1.3_0123456789abcdef"
	c := &fakeCore{modelDirs: map[string]string{"astrlink-guard-int8": dir}}
	h := &fakeHost{}
	s := NewService(opener(c, nil), h)
	if err := s.OpenModelDirectory("astrlink-guard-int8"); err != nil || h.opened != dir {
		t.Fatalf("%q %v", h.opened, err)
	}
	h.opened, h.err = "", errors.New("open /Users/x: denied")
	if err := s.OpenModelDirectory("astrlink-guard-int8"); err == nil || err.Error() != "INTERNAL_ERROR" {
		t.Fatalf("the host's message must not leak: %v", err)
	}
	h.opened, h.err = "", nil
	if err := s.OpenModelDirectory("privacy-filter-int8"); err == nil || err.Error() != "MODEL_NOT_INSTALLED" || h.opened != "" {
		t.Fatalf("a model without files opens nothing: %q %v", h.opened, err)
	}
	if err := NewService(nil, nil).OpenModelDirectory("astrlink-guard-int8"); err == nil {
		t.Fatal("missing host must fail")
	}
}

func TestModelCommandsForwardTheIDAndKeepOnlyTheCode(t *testing.T) {
	c := &fakeCore{}
	s := NewService(opener(c, nil), &fakeHost{})
	if err := s.DownloadModel("privacy-filter-int8"); err != nil || c.asked[len(c.asked)-1] != "download:privacy-filter-int8" {
		t.Fatalf("%v %v", c.asked, err)
	}
	c.err = errors.New("MODEL_MEMORY_INSUFFICIENT: 8 GiB at /Users/x")
	if err := s.DownloadModel("privacy-filter-int8"); err == nil || err.Error() != "MODEL_MEMORY_INSUFFICIENT" {
		t.Fatal(err)
	}
}
