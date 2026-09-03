package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// errBoom is the canonical injected error for error-path tests.
var errBoom = errors.New("boom")

// This test file establishes unit coverage for the ActionD MCP tool layer
// (internal/mcp). Handlers are package-level free functions that take their
// dependencies (ActionDClient / LifecycleController) as explicit leading
// args, so each is invoked directly with a fake — no daemon, no MCP transport.
//
// House style (matched to existing *_test.go in this repo): stdlib only
// (t.Errorf/t.Fatalf), same internal package so unexported handlers are
// reachable, one-func-per-case (table-driven only for the lifecycle env gate,
// which is a pure boolean dispatch).

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeToggle struct {
	name    string
	enabled bool
}

type fakeCleanupCall struct {
	days int
	all  bool
}

// fakeClient implements ActionDClient. Each method returns canned data unless
// its per-method error field is set; mutate calls are recorded.
type fakeClient struct {
	// canned read data
	plugins        []PluginInfo
	actions        []ActionInfo
	actionsByEvent map[string][]ActionInfo
	actionDetail   map[string]*ActionDetail
	status         *StatusInfo
	logs           []LogEntry
	profile        string
	reloadResult   *ReloadResult

	// per-method error injection
	getPluginsErr     error
	getActionsErr     error
	getActionErr      error
	actionsByEventErr error
	reloadErr         error
	statusErr         error
	logsErr           error
	cancelErr         error
	retryErr          error
	setPluginErr      error
	getProfileErr     error
	setProfileErr     error

	// call recorders
	getActionArgs  []string
	cancelledIDs   []string
	retriedIDs     []string
	toggled        []fakeToggle
	setProfileArgs []string
	reloadCalls    int
	cleanupCalls   []fakeCleanupCall
	cleanupResult  *CleanupResult
	cleanupErr     error
}

func (f *fakeClient) GetPlugins() ([]PluginInfo, error) {
	if f.getPluginsErr != nil {
		return nil, f.getPluginsErr
	}
	if f.plugins == nil {
		return []PluginInfo{}, nil
	}
	return f.plugins, nil
}

func (f *fakeClient) GetActions(int) ([]ActionInfo, error) {
	if f.getActionsErr != nil {
		return nil, f.getActionsErr
	}
	if f.actions == nil {
		return []ActionInfo{}, nil
	}
	return f.actions, nil
}

func (f *fakeClient) GetActionsByEventID(eventID string) ([]ActionInfo, error) {
	if f.actionsByEventErr != nil {
		return nil, f.actionsByEventErr
	}
	if f.actionsByEvent != nil {
		return f.actionsByEvent[eventID], nil
	}
	return []ActionInfo{}, nil
}

func (f *fakeClient) GetAction(id string) (*ActionDetail, error) {
	f.getActionArgs = append(f.getActionArgs, id)
	if f.getActionErr != nil {
		return nil, f.getActionErr
	}
	if f.actionDetail != nil {
		if d, ok := f.actionDetail[id]; ok {
			return d, nil
		}
	}
	// default: a cancellable, retryable running job
	return &ActionDetail{ID: id, Status: "running", PluginName: "fake-plugin", Repo: "fake-repo"}, nil
}

func (f *fakeClient) ReloadPlugins() (*ReloadResult, error) {
	f.reloadCalls++
	if f.reloadErr != nil {
		return nil, f.reloadErr
	}
	if f.reloadResult != nil {
		return f.reloadResult, nil
	}
	return &ReloadResult{Status: "ok", Count: 1, PluginList: []string{"go-lint"}}, nil
}

func (f *fakeClient) GetStatus() (*StatusInfo, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	if f.status != nil {
		return f.status, nil
	}
	return &StatusInfo{Running: true, Version: "test", Uptime: "1s", PluginCount: 1}, nil
}

func (f *fakeClient) GetLogs(int) ([]LogEntry, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	if f.logs == nil {
		return []LogEntry{}, nil
	}
	return f.logs, nil
}

func (f *fakeClient) CancelAction(id string) error {
	f.cancelledIDs = append(f.cancelledIDs, id)
	return f.cancelErr
}

func (f *fakeClient) RetryAction(id string) error {
	f.retriedIDs = append(f.retriedIDs, id)
	return f.retryErr
}

func (f *fakeClient) SetPluginEnabled(name string, enabled bool) error {
	f.toggled = append(f.toggled, fakeToggle{name, enabled})
	return f.setPluginErr
}

func (f *fakeClient) GetProfile() (string, error) {
	if f.getProfileErr != nil {
		return "", f.getProfileErr
	}
	return f.profile, nil
}

func (f *fakeClient) SetProfile(profile string) error {
	f.setProfileArgs = append(f.setProfileArgs, profile)
	return f.setProfileErr
}

func (f *fakeClient) CleanupActions(days int, all bool) (*CleanupResult, error) {
	f.cleanupCalls = append(f.cleanupCalls, fakeCleanupCall{days, all})
	if f.cleanupErr != nil {
		return nil, f.cleanupErr
	}
	if f.cleanupResult != nil {
		return f.cleanupResult, nil
	}
	return &CleanupResult{Status: "success", DeletedJobs: 0, DeletedDirs: 0, RetentionDays: days}, nil
}

// fakeLifecycle implements LifecycleController. Start/Stop flip the running
// flag (unless an error is configured) so waitForRunningState terminates fast.
type fakeLifecycle struct {
	running    bool
	startErr   error
	stopErr    error
	startCalls int
	stopCalls  int
}

func (l *fakeLifecycle) Start(context.Context) (string, error) {
	l.startCalls++
	if l.startErr != nil {
		return "", l.startErr
	}
	l.running = true
	return "started", nil
}

func (l *fakeLifecycle) Stop(context.Context) (string, error) {
	l.stopCalls++
	if l.stopErr != nil {
		return "", l.stopErr
	}
	l.running = false
	return "stopped", nil
}

func (l *fakeLifecycle) IsRunning() bool { return l.running }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func callReq(args map[string]any) mcp.CallToolRequest {
	var r mcp.CallToolRequest
	if args != nil {
		r.Params.Arguments = args
	}
	return r
}

// bodyText extracts the first TextContent payload from a CallToolResult.
func bodyText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatalf("nil result")
	}
	if len(r.Content) == 0 {
		t.Fatalf("result has no content (IsError=%v)", r.IsError)
	}
	if tc, ok := r.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	t.Fatalf("result content[0] is not TextContent: %T", r.Content[0])
	return ""
}

func wantOK(t *testing.T, r *mcp.CallToolResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if r == nil {
		t.Fatalf("nil result")
	}
	if r.IsError {
		t.Fatalf("expected success, got IsError: %s", bodyText(t, r))
	}
}

func wantErr(t *testing.T, r *mcp.CallToolResult, err error, substr ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if r == nil {
		t.Fatalf("nil result")
	}
	if !r.IsError {
		t.Fatalf("expected IsError, got success: %s", bodyText(t, r))
	}
	body := bodyText(t, r)
	for _, s := range substr {
		if !strings.Contains(body, s) {
			t.Errorf("error body %q does not contain %q", body, s)
		}
	}
}

// ---------------------------------------------------------------------------
// Smoke
// ---------------------------------------------------------------------------

func TestNewServerSmoke(t *testing.T) {
	s := NewServer(&fakeClient{}, &fakeLifecycle{}, &fakeLGH{})
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

// ---------------------------------------------------------------------------
// actiond_status
// ---------------------------------------------------------------------------

func TestHandleStatus(t *testing.T) {
	r, err := handleStatus(&fakeClient{}, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "running") {
		t.Errorf("status body missing 'running': %s", body)
	}
}

func TestHandleStatusError(t *testing.T) {
	r, err := handleStatus(&fakeClient{statusErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to get status")
}

// ---------------------------------------------------------------------------
// actiond_plugins_list
// ---------------------------------------------------------------------------

func TestHandlePluginsList(t *testing.T) {
	fc := &fakeClient{plugins: []PluginInfo{{Name: "go-lint", Type: "exec"}}}
	r, err := handlePluginsList(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "go-lint") {
		t.Errorf("plugins body missing 'go-lint': %s", body)
	}
}

func TestHandlePluginsListError(t *testing.T) {
	r, err := handlePluginsList(&fakeClient{getPluginsErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to list plugins")
}

// ---------------------------------------------------------------------------
// actiond_actions_list
// ---------------------------------------------------------------------------

func TestHandleActionsList(t *testing.T) {
	fc := &fakeClient{actions: []ActionInfo{{ID: "act-1", Repo: "r", PluginName: "p", Status: "done"}}}
	r, err := handleActionsList(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "act-1") {
		t.Errorf("actions body missing 'act-1': %s", body)
	}
}

func TestHandleActionsListError(t *testing.T) {
	r, err := handleActionsList(&fakeClient{getActionsErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to list actions")
}

// ---------------------------------------------------------------------------
// actiond_action_get
// ---------------------------------------------------------------------------

func TestHandleActionGet(t *testing.T) {
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{"j-1": {ID: "j-1", Status: "done"}}}
	r, err := handleActionGet(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "j-1") {
		t.Errorf("action body missing 'j-1': %s", body)
	}
	if len(fc.getActionArgs) != 1 || fc.getActionArgs[0] != "j-1" {
		t.Errorf("GetAction called with %v, want [j-1]", fc.getActionArgs)
	}
}

func TestHandleActionGetMissingID(t *testing.T) {
	r, err := handleActionGet(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleActionGetEmptyID(t *testing.T) {
	// empty string is present but -> args["id"] is "" which is a valid string,
	// so GetAction is called with "". The default fake returns a running job.
	r, err := handleActionGet(&fakeClient{}, context.Background(), callReq(map[string]any{"id": ""}))
	wantOK(t, r, err)
}

func TestHandleActionGetWrongType(t *testing.T) {
	r, err := handleActionGet(&fakeClient{}, context.Background(), callReq(map[string]any{"id": 123}))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleActionGetError(t *testing.T) {
	r, err := handleActionGet(&fakeClient{getActionErr: errBoom}, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Failed to get action")
}

// ---------------------------------------------------------------------------
// actiond_plugins_reload
// ---------------------------------------------------------------------------

func TestHandlePluginsReload(t *testing.T) {
	fc := &fakeClient{}
	r, err := handlePluginsReload(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if fc.reloadCalls != 1 {
		t.Errorf("ReloadPlugins called %d times, want 1", fc.reloadCalls)
	}
}

func TestHandlePluginsReloadError(t *testing.T) {
	r, err := handlePluginsReload(&fakeClient{reloadErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to reload plugins")
}

// ---------------------------------------------------------------------------
// actiond_log
// ---------------------------------------------------------------------------

func TestHandleLog(t *testing.T) {
	fc := &fakeClient{logs: []LogEntry{{Level: "info", Message: "hello world"}}}
	r, err := handleLog(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "hello world") {
		t.Errorf("log body missing message: %s", body)
	}
}

func TestHandleLogError(t *testing.T) {
	r, err := handleLog(&fakeClient{logsErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to get logs")
}

// ---------------------------------------------------------------------------
// actiond_job_cancel
// ---------------------------------------------------------------------------

func TestHandleJobCancel(t *testing.T) {
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{
		"j-1": {ID: "j-1", Status: "running", PluginName: "go-lint"},
	}}
	r, err := handleJobCancel(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "Job cancelled") {
		t.Errorf("cancel body missing success message: %s", body)
	}
	if len(fc.cancelledIDs) != 1 || fc.cancelledIDs[0] != "j-1" {
		t.Errorf("CancelAction called with %v, want [j-1]", fc.cancelledIDs)
	}
}

func TestHandleJobCancelMissingID(t *testing.T) {
	r, err := handleJobCancel(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleJobCancelDoneStatus(t *testing.T) {
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{
		"j-1": {ID: "j-1", Status: "done"},
	}}
	r, err := handleJobCancel(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Cannot cancel job with status: done")
	if len(fc.cancelledIDs) != 0 {
		t.Errorf("CancelAction should not be called for terminal job, got %v", fc.cancelledIDs)
	}
}

func TestHandleJobCancelGetActionError(t *testing.T) {
	r, err := handleJobCancel(&fakeClient{getActionErr: errBoom}, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Job not found")
}

func TestHandleJobCancelError(t *testing.T) {
	fc := &fakeClient{
		actionDetail: map[string]*ActionDetail{"j-1": {ID: "j-1", Status: "running"}},
		cancelErr:    errBoom,
	}
	r, err := handleJobCancel(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Failed to cancel job")
}

// ---------------------------------------------------------------------------
// actiond_job_retry
// ---------------------------------------------------------------------------

func TestHandleJobRetry(t *testing.T) {
	fc := &fakeClient{actionDetail: map[string]*ActionDetail{
		"j-1": {ID: "j-1", Status: "failed", PluginName: "go-test"},
	}}
	r, err := handleJobRetry(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "queued for retry") {
		t.Errorf("retry body missing success message: %s", body)
	}
	if len(fc.retriedIDs) != 1 || fc.retriedIDs[0] != "j-1" {
		t.Errorf("RetryAction called with %v, want [j-1]", fc.retriedIDs)
	}
}

func TestHandleJobRetryMissingID(t *testing.T) {
	r, err := handleJobRetry(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: id")
}

func TestHandleJobRetryError(t *testing.T) {
	fc := &fakeClient{retryErr: errBoom}
	r, err := handleJobRetry(fc, context.Background(), callReq(map[string]any{"id": "j-1"}))
	wantErr(t, r, err, "Failed to retry job")
}

// ---------------------------------------------------------------------------
// actiond_plugin_enable / actiond_plugin_disable
// ---------------------------------------------------------------------------

func TestHandlePluginEnable(t *testing.T) {
	fc := &fakeClient{}
	r, err := handlePluginToggle(fc, context.Background(), callReq(map[string]any{"name": "go-lint"}), true)
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "enabled") {
		t.Errorf("enable body missing 'enabled': %s", body)
	}
	if len(fc.toggled) != 1 || !fc.toggled[0].enabled || fc.toggled[0].name != "go-lint" {
		t.Errorf("SetPluginEnabled calls = %v, want [{go-lint true}]", fc.toggled)
	}
}

func TestHandlePluginDisable(t *testing.T) {
	fc := &fakeClient{}
	r, err := handlePluginToggle(fc, context.Background(), callReq(map[string]any{"name": "go-lint"}), false)
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "disabled") {
		t.Errorf("disable body missing 'disabled': %s", body)
	}
	if len(fc.toggled) != 1 || fc.toggled[0].enabled {
		t.Errorf("SetPluginEnabled calls = %v, want [{go-lint false}]", fc.toggled)
	}
}

func TestHandlePluginToggleMissingName(t *testing.T) {
	r, err := handlePluginToggle(&fakeClient{}, context.Background(), callReq(nil), true)
	wantErr(t, r, err, "Missing required parameter: name")
}

func TestHandlePluginToggleError(t *testing.T) {
	fc := &fakeClient{setPluginErr: errBoom}
	r, err := handlePluginToggle(fc, context.Background(), callReq(map[string]any{"name": "go-lint"}), true)
	wantErr(t, r, err, "Failed to toggle plugin")
}

// ---------------------------------------------------------------------------
// actiond_profile_get / actiond_profile_set
// ---------------------------------------------------------------------------

func TestHandleProfileGet(t *testing.T) {
	fc := &fakeClient{profile: "fast"}
	r, err := handleProfileGet(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	body := bodyText(t, r)
	if !strings.Contains(body, "fast") {
		t.Errorf("profile body missing 'fast': %s", body)
	}
	if !strings.Contains(body, "Minimal CI") {
		t.Errorf("profile body missing description: %s", body)
	}
}

func TestHandleProfileGetError(t *testing.T) {
	r, err := handleProfileGet(&fakeClient{getProfileErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to get profile")
}

func TestHandleProfileSet(t *testing.T) {
	for _, p := range []string{"fast", "full", "release"} {
		fc := &fakeClient{}
		r, err := handleProfileSet(fc, context.Background(), callReq(map[string]any{"profile": p}))
		wantOK(t, r, err)
		if len(fc.setProfileArgs) != 1 || fc.setProfileArgs[0] != p {
			t.Errorf("profile=%q: SetProfile args %v, want [%s]", p, fc.setProfileArgs, p)
		}
	}
}

func TestHandleProfileSetMissing(t *testing.T) {
	r, err := handleProfileSet(&fakeClient{}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Missing required parameter: profile")
}

func TestHandleProfileSetInvalid(t *testing.T) {
	r, err := handleProfileSet(&fakeClient{}, context.Background(), callReq(map[string]any{"profile": "bogus"}))
	wantErr(t, r, err, "Invalid profile")
}

func TestHandleProfileSetError(t *testing.T) {
	fc := &fakeClient{setProfileErr: errBoom}
	r, err := handleProfileSet(fc, context.Background(), callReq(map[string]any{"profile": "fast"}))
	wantErr(t, r, err, "Failed to set profile")
}

// ---------------------------------------------------------------------------
// actiond_plugins_recommend
// ---------------------------------------------------------------------------

func TestHandlePluginsRecommendMissingPathDefaults(t *testing.T) {
	// No path -> handler defaults to "." and does NOT error.
	r, err := handlePluginsRecommend(&fakeClient{}, context.Background(), callReq(nil))
	wantOK(t, r, err)
}

func TestHandlePluginsRecommendError(t *testing.T) {
	r, err := handlePluginsRecommend(&fakeClient{getPluginsErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to get plugins")
}

func TestHandlePluginsRecommendGoProject(t *testing.T) {
	// A temp dir with a go.mod triggers Go language detection -> go-lint recommended.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := handlePluginsRecommend(&fakeClient{}, context.Background(), callReq(map[string]any{"path": dir}))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "go-lint") {
		t.Errorf("recommend body missing 'go-lint' for go project: %s", body)
	}
}

// ---------------------------------------------------------------------------
// actiond_diagnose
// ---------------------------------------------------------------------------

func TestHandleDiagnoseByJobID(t *testing.T) {
	fc := &fakeClient{
		actionDetail: map[string]*ActionDetail{
			"j-1": {ID: "j-1", Status: "failed", PluginName: "go-build", Repo: "demo"},
		},
		logs: []LogEntry{{Level: "error", Message: "j-1: build failed: undefined: foo in main.go:12"}},
	}
	r, err := handleDiagnose(fc, context.Background(), callReq(map[string]any{"job_id": "j-1"}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	if !strings.Contains(body, "j-1") {
		t.Errorf("diagnose body missing job id: %s", body)
	}
	if !strings.Contains(body, "total_analyzed") {
		t.Errorf("diagnose body missing total_analyzed: %s", body)
	}
	// Structured feedback fields for AI consumers.
	for _, want := range []string{`"category"`, `"confidence"`, `"evidence_lines"`, "go_build_failed", "main.go:12"} {
		if !strings.Contains(body, want) {
			t.Errorf("diagnose body missing %q: %s", want, body)
		}
	}
}

// TestHandleDiagnoseFiltersOtherJobs ensures single-job diagnosis only
// consumes log lines that reference the requested job id.
func TestHandleDiagnoseFiltersOtherJobs(t *testing.T) {
	fc := &fakeClient{
		actionDetail: map[string]*ActionDetail{
			"j-1": {ID: "j-1", Status: "failed", PluginName: "go-test", Repo: "demo"},
		},
		logs: []LogEntry{
			{Level: "error", Message: "j-9: undefined: bar in other.go:3"},
			{Level: "error", Message: "j-1: context deadline exceeded"},
		},
	}
	r, err := handleDiagnose(fc, context.Background(), callReq(map[string]any{"job_id": "j-1"}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	if strings.Contains(body, "j-9") {
		t.Errorf("diagnose body leaked another job's log: %s", body)
	}
	if !strings.Contains(body, `"category": "timeout"`) {
		t.Errorf("diagnose body missing j-1 timeout diagnosis: %s", body)
	}
}

// TestHandleDiagnoseNoErrorOutput reports a gap explicitly instead of
// silently dropping the job from diagnose_summary.
func TestHandleDiagnoseNoErrorOutput(t *testing.T) {
	fc := &fakeClient{
		actionDetail: map[string]*ActionDetail{
			"j-1": {ID: "j-1", Status: "failed", PluginName: "go-build", Repo: "demo"},
		},
		logs: []LogEntry{{Level: "info", Message: "j-1: started"}},
	}
	r, err := handleDiagnose(fc, context.Background(), callReq(map[string]any{"job_id": "j-1"}))
	wantOK(t, r, err)
	body := bodyText(t, r)
	if !strings.Contains(body, "no_error_output") {
		t.Errorf("diagnose body missing no_error_output placeholder: %s", body)
	}
}

func TestHandleDiagnoseRecentFailed(t *testing.T) {
	fc := &fakeClient{
		actions: []ActionInfo{{ID: "j-2", Repo: "demo", PluginName: "go-test", Status: "failed"}},
	}
	r, err := handleDiagnose(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "j-2") {
		t.Errorf("diagnose body missing failed job j-2: %s", body)
	}
}

func TestHandleDiagnoseGetActionError(t *testing.T) {
	r, err := handleDiagnose(&fakeClient{getActionErr: errBoom}, context.Background(), callReq(map[string]any{"job_id": "j-1"}))
	wantErr(t, r, err, "Failed to get job")
}

func TestHandleDiagnoseGetActionsError(t *testing.T) {
	r, err := handleDiagnose(&fakeClient{getActionsErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to get actions")
}

// ---------------------------------------------------------------------------
// actiond_server_start / stop / restart (lifecycle)
// ---------------------------------------------------------------------------

// TestLifecycleHandlersRequireEnvGate: with ACTIOND_MCP_ALLOW_LIFECYCLE unset,
// every lifecycle handler refuses and never touches Start/Stop.
func TestLifecycleHandlersRequireEnvGate(t *testing.T) {
	t.Setenv("ACTIOND_MCP_ALLOW_LIFECYCLE", "")
	fc := &fakeClient{}
	fl := &fakeLifecycle{}
	ctx := context.Background()

	cases := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{"start", func() (*mcp.CallToolResult, error) { return handleServerStart(fc, fl, ctx, callReq(nil)) }},
		{"stop", func() (*mcp.CallToolResult, error) { return handleServerStop(fc, fl, ctx, callReq(nil)) }},
		{"restart", func() (*mcp.CallToolResult, error) { return handleServerRestart(fc, fl, ctx, callReq(nil)) }},
	}
	for _, c := range cases {
		r, err := c.call()
		wantErr(t, r, err, "lifecycle control is disabled")
		if fl.startCalls != 0 || fl.stopCalls != 0 {
			t.Errorf("%s: Start/Stop must not run when gate disabled (start=%d stop=%d)",
				c.name, fl.startCalls, fl.stopCalls)
		}
	}
}

func withLifecycleEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ACTIOND_MCP_ALLOW_LIFECYCLE", "1")
}

func TestHandleServerStart(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: false}
	r, err := handleServerStart(&fakeClient{}, fl, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if fl.startCalls != 1 {
		t.Errorf("Start called %d times, want 1", fl.startCalls)
	}
	if body := bodyText(t, r); !strings.Contains(body, "ActionD started") {
		t.Errorf("start body: %s", body)
	}
}

func TestHandleServerStartAlreadyRunning(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: true}
	r, err := handleServerStart(&fakeClient{}, fl, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if fl.startCalls != 0 {
		t.Errorf("Start must not be called when already running, got %d", fl.startCalls)
	}
	if body := bodyText(t, r); !strings.Contains(body, "already running") {
		t.Errorf("start body missing 'already running': %s", body)
	}
}

func TestHandleServerStartError(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{startErr: errBoom}
	r, err := handleServerStart(&fakeClient{}, fl, context.Background(), callReq(nil))
	// Start failure is reported inside the result body (changed=false), not as IsError.
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "Failed to start ActionD") {
		t.Errorf("start body missing failure message: %s", body)
	}
}

func TestHandleServerStopAlreadyStopped(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: false}
	r, err := handleServerStop(&fakeClient{}, fl, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if fl.stopCalls != 0 {
		t.Errorf("Stop must not be called when already stopped, got %d", fl.stopCalls)
	}
	if body := bodyText(t, r); !strings.Contains(body, "already stopped") {
		t.Errorf("stop body: %s", body)
	}
}

func TestHandleServerStopRefusesWithActiveJobs(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: true}
	fc := &fakeClient{actions: []ActionInfo{{ID: "run-1", Status: "running"}}}
	r, err := handleServerStop(fc, fl, context.Background(), callReq(map[string]any{"force": false}))
	wantErr(t, r, err, "Refusing to stop")
	if fl.stopCalls != 0 {
		t.Errorf("Stop must not run when refusing, got %d", fl.stopCalls)
	}
}

func TestHandleServerStopForce(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: true}
	fc := &fakeClient{actions: []ActionInfo{{ID: "run-1", Status: "running"}}}
	r, err := handleServerStop(fc, fl, context.Background(), callReq(map[string]any{"force": true}))
	wantOK(t, r, err)
	if fl.stopCalls != 1 {
		t.Errorf("Stop called %d times, want 1", fl.stopCalls)
	}
	if body := bodyText(t, r); !strings.Contains(body, "ActionD stopped") {
		t.Errorf("stop body: %s", body)
	}
}

func TestHandleServerRestartForce(t *testing.T) {
	withLifecycleEnv(t)
	fl := &fakeLifecycle{running: true}
	fc := &fakeClient{actions: []ActionInfo{{ID: "run-1", Status: "running"}}}
	r, err := handleServerRestart(fc, fl, context.Background(), callReq(map[string]any{"force": true}))
	wantOK(t, r, err)
	if fl.stopCalls != 1 || fl.startCalls != 1 {
		t.Errorf("restart: stop=%d start=%d, want 1/1", fl.stopCalls, fl.startCalls)
	}
}

// ---------------------------------------------------------------------------
// withInteger (schema narrowing helper)
// ---------------------------------------------------------------------------

func TestWithInteger(t *testing.T) {
	tool := mcp.NewTool("test_tool",
		mcp.WithNumber("timeout", mcp.Description("secs")),
		withInteger("timeout"),
		mcp.WithNumber("plain"),
	)

	narrowed, ok := tool.InputSchema.Properties["timeout"].(map[string]any)
	if !ok {
		t.Fatalf("timeout property missing or wrong type: %T", tool.InputSchema.Properties["timeout"])
	}
	if narrowed["type"] != "integer" {
		t.Errorf("timeout type = %v, want integer", narrowed["type"])
	}

	plain, ok := tool.InputSchema.Properties["plain"].(map[string]any)
	if !ok {
		t.Fatalf("plain property missing: %T", tool.InputSchema.Properties["plain"])
	}
	if plain["type"] != "number" {
		t.Errorf("plain type = %v, want number (untouched)", plain["type"])
	}
}

// ---------------------------------------------------------------------------
// actiond_cleanup
// ---------------------------------------------------------------------------

func TestHandleCleanupDefaults(t *testing.T) {
	fc := &fakeClient{cleanupResult: &CleanupResult{Status: "success", DeletedJobs: 3, DeletedDirs: 4, RetentionDays: 7}}
	r, err := handleCleanup(fc, context.Background(), callReq(nil))
	wantOK(t, r, err)
	if body := bodyText(t, r); !strings.Contains(body, "DeletedJobs") && !strings.Contains(body, "deleted_jobs") {
		t.Errorf("cleanup body missing counts: %s", body)
	}
	if len(fc.cleanupCalls) != 1 || fc.cleanupCalls[0].days != 7 || fc.cleanupCalls[0].all {
		t.Errorf("cleanup called with %v, want days=7 all=false", fc.cleanupCalls)
	}
}

func TestHandleCleanupAll(t *testing.T) {
	fc := &fakeClient{}
	r, err := handleCleanup(fc, context.Background(), callReq(map[string]any{"all": true, "days": 0}))
	wantOK(t, r, err)
	if len(fc.cleanupCalls) != 1 || !fc.cleanupCalls[0].all || fc.cleanupCalls[0].days != 0 {
		t.Errorf("cleanup called with %v, want days=0 all=true", fc.cleanupCalls)
	}
}

func TestHandleCleanupError(t *testing.T) {
	r, err := handleCleanup(&fakeClient{cleanupErr: errBoom}, context.Background(), callReq(nil))
	wantErr(t, r, err, "Failed to cleanup actions")
}
