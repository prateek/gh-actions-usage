package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	responses map[string]string
	requests  []string
}

func (f *fakeClient) Get(path string, resp interface{}) error {
	f.requests = append(f.requests, path)
	data, ok := f.responses[path]
	if !ok {
		data = f.responses[strings.Split(path, "&page=")[0]]
	}
	return json.Unmarshal([]byte(data), resp)
}

func TestCreatedQuery(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		since string
		until string
		days  int
		want  string
	}{
		{name: "days", days: 7, want: ">=2026-04-17"},
		{name: "since", since: "2026-04-01", days: 30, want: ">=2026-04-01"},
		{name: "until", until: "2026-04-10", days: 30, want: "<=2026-04-10"},
		{name: "range", since: "2026-04-01", until: "2026-04-10", days: 30, want: "2026-04-01..2026-04-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := createdQuery(tt.since, tt.until, tt.days, now)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("created query = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRepoUnmarshalCapturesOwnerAndRaw(t *testing.T) {
	var repo Repo
	if err := json.Unmarshal([]byte(`{"id":1,"name":"app","full_name":"octo/app","private":true,"owner":{"login":"octo"}}`), &repo); err != nil {
		t.Fatal(err)
	}
	if repo.Owner != "octo" || repo.FullName != "octo/app" || !repo.Private {
		t.Fatalf("repo = %#v", repo)
	}
	if len(repo.Raw) == 0 {
		t.Fatal("raw JSON was not captured")
	}
}

func TestRunnerMetadata(t *testing.T) {
	got := runnerMetadata([]string{"self-hosted", "macOS", "ARM64"})
	if got.Type != "self-hosted" || got.OS != "macOS" || got.Arch != "ARM64" {
		t.Fatalf("metadata = %#v", got)
	}
}

func TestBillingUsageEndpointSupportsEnterpriseFilters(t *testing.T) {
	got, err := billingUsageEndpoint(
		accountContext{Kind: "enterprise", Login: "acme"},
		BillingQueryFilters{Year: 2026, Month: 4, Organization: "demo-org", Repo: "demo-org/mobile", Product: "Actions", SKU: "actions_macos", CostCenterID: "none"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "enterprises/acme/settings/billing/usage?year=2026&month=4&organization=demo-org&repository=demo-org%2Fmobile&product=Actions&sku=actions_macos&cost_center_id=none"
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestDefaultCachePathUsesExplicitOverride(t *testing.T) {
	t.Setenv("GH_ACTIONS_USAGE_CACHE", "/tmp/custom-cache.db")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))

	got := defaultCachePath()
	if got != "/tmp/custom-cache.db" {
		t.Fatalf("default cache path = %q, want explicit override", got)
	}
}

func TestDefaultCachePathUsesXDGCacheHome(t *testing.T) {
	xdgCacheHome := filepath.Join(t.TempDir(), "xdg-cache")
	t.Setenv("GH_ACTIONS_USAGE_CACHE", "")
	t.Setenv("XDG_CACHE_HOME", xdgCacheHome)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	want := filepath.Join(xdgCacheHome, appName, "cache.db")
	if got := defaultCachePath(); got != want {
		t.Fatalf("default cache path = %q, want %q", got, want)
	}
}

func TestDefaultCachePathFallsBackToHomeCache(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("GH_ACTIONS_USAGE_CACHE", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".cache", appName, "cache.db")
	if got := defaultCachePath(); got != want {
		t.Fatalf("default cache path = %q, want %q", got, want)
	}
}

func TestDefaultCachePathIgnoresRelativeXDGCacheHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("GH_ACTIONS_USAGE_CACHE", "")
	t.Setenv("XDG_CACHE_HOME", "relative-cache")
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".cache", appName, "cache.db")
	if got := defaultCachePath(); got != want {
		t.Fatalf("default cache path = %q, want %q", got, want)
	}
}

func TestCacheUpsertsAreIdempotent(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	repo := Repo{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app", Private: true}
	run := RunRecord{ID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: ".github/workflows/ci.yml", RunStartedAt: "2026-04-24T10:00:00Z", Conclusion: "success"}
	job := JobRecord{
		ID:           100,
		RunID:        10,
		Repo:         "octo/app",
		WorkflowName: "CI",
		WorkflowPath: ".github/workflows/ci.yml",
		Name:         "test",
		Conclusion:   "success",
		StartedAt:    "2026-04-24T10:00:00Z",
		CompletedAt:  "2026-04-24T10:02:00Z",
		DurationSecs: 120,
		Runner:       runnerMetadata([]string{"ubuntu-latest"}),
		Labels:       []string{"ubuntu-latest"},
	}

	for i := 0; i < 2; i++ {
		if err := cache.UpsertRepo(repo); err != nil {
			t.Fatal(err)
		}
		if err := cache.UpsertRun(run); err != nil {
			t.Fatal(err)
		}
		if err := cache.UpsertJob(job); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := cache.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["repos"] != 1 || stats["runs"] != 1 || stats["jobs"] != 1 {
		t.Fatalf("stats = %#v, want one row per table", stats)
	}
}

func TestOpenCacheCreatesParentDirectoryPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache-home", appName)
	cache, err := OpenCache(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("cache directory mode = %o, want 700", got)
	}
}

func TestSummaryGroupsByWorkflowAndRunner(t *testing.T) {
	jobs := []JobRecord{
		{ID: 1, RunID: 1, Repo: "octo/app", WorkflowPath: ".github/workflows/ci.yml", Name: "test", DurationSecs: 120, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"},
		{ID: 2, RunID: 1, Repo: "octo/app", WorkflowPath: ".github/workflows/ci.yml", Name: "snapshot", DurationSecs: 300, Runner: RunnerMetadata{Image: "macos-15"}, Conclusion: "failure"},
	}
	summary := buildSummary("/tmp/cache.db", jobs, QueryFilters{}, []string{"repo", "workflow-path", "runner-image"}, time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC))
	if summary.TotalJobs != 2 || summary.TotalRuns != 1 || summary.TotalSeconds != 420 {
		t.Fatalf("summary totals = %#v", summary)
	}
	if len(summary.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(summary.Groups))
	}
	if summary.Groups[0].Values["runner-image"] != "macos-15" {
		t.Fatalf("first group = %#v, want macos group first", summary.Groups[0])
	}
}

func TestRunSummaryCommandReadsCache(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	if err := cache.UpsertJob(JobRecord{ID: 1, RunID: 1, Repo: "octo/app", Name: "test", WorkflowPath: "ci.yml", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: &bytes.Buffer{}, cache: cache, now: func() time.Time { return time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC) }}
	if err := app.Run(t.Context(), []string{"summary", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"total_jobs": 1`) {
		t.Fatalf("summary output = %s", out.String())
	}
}

func TestReportCommandRefreshesAndSummarizes(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	app := &App{
		stdout: &out,
		stderr: &bytes.Buffer{},
		cache:  cache,
		client: fakeActionsClient(),
		now:    func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
	}
	if err := app.Run(t.Context(), []string{"report", "--account", "@me", "--repo", "octo/app", "--since", "2026-04-01", "--group-by", "repo,billing-owner", "--json"}); err != nil {
		t.Fatal(err)
	}

	var payload ReportResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("report output is not JSON: %s", out.String())
	}
	if payload.Refresh.RunsUpserted != 1 || payload.Refresh.JobsUpserted != 1 {
		t.Fatalf("refresh = %#v, want one run and one job", payload.Refresh)
	}
	if payload.Summary.TotalJobs != 1 || payload.Summary.TotalMinutes != 2 {
		t.Fatalf("summary = %#v, want one two-minute job", payload.Summary)
	}
	if payload.Summary.Groups[0].Values["billing-owner"] != "octo" {
		t.Fatalf("summary groups = %#v, want billing owner attribution", payload.Summary.Groups)
	}
}

func TestReportCommandRefreshesMultipleAccounts(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	app := &App{
		stdout: &out,
		stderr: &bytes.Buffer{},
		cache:  cache,
		client: fakeActionsClient(),
		now:    func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
	}
	if err := app.Run(t.Context(), []string{
		"report",
		"--account", "@me,demo-org",
		"--since", "2026-04-01",
		"--account-plan", "octo=pro,demo-org=enterprise",
		"--billing-owner", "demo-org=acme-enterprise",
		"--billing-kind", "demo-org=enterprise",
		"--group-by", "account,billing-owner,billing-owner-kind,billing-plan,repo-owner,repo",
		"--json",
	}); err != nil {
		t.Fatal(err)
	}

	var payload ReportResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("report output is not JSON: %s", out.String())
	}
	if payload.Refresh.AccountsIngested != 2 || payload.Refresh.ReposIngested != 2 || payload.Summary.TotalJobs != 2 {
		t.Fatalf("report = %#v, want two account refresh", payload)
	}
	groups := map[string]SummaryGroup{}
	for _, group := range payload.Summary.Groups {
		groups[group.Values["repo"]] = group
	}
	if groups["octo/app"].Values["billing-owner"] != "octo" || groups["octo/app"].Values["billing-plan"] != "pro" {
		t.Fatalf("personal repo group = %#v", groups["octo/app"])
	}
	if groups["demo-org/mobile"].Values["billing-owner"] != "acme-enterprise" || groups["demo-org/mobile"].Values["billing-owner-kind"] != "enterprise" {
		t.Fatalf("org repo group = %#v", groups["demo-org/mobile"])
	}
}

func TestReportCommandDefaultDaysFiltersCachedRows(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	if err := cache.UpsertJob(JobRecord{ID: 99, RunID: 9, Repo: "octo/app", Name: "old", WorkflowPath: "ci.yml", StartedAt: "2026-03-01T10:00:00Z", CompletedAt: "2026-03-01T10:05:00Z", DurationSecs: 300, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{
		stdout: &out,
		stderr: &bytes.Buffer{},
		cache:  cache,
		client: fakeActionsClient(),
		now:    func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
	}
	if err := app.Run(t.Context(), []string{"report", "--account", "@me", "--repo", "octo/app", "--json"}); err != nil {
		t.Fatal(err)
	}

	var payload ReportResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("report output is not JSON: %s", out.String())
	}
	if payload.Summary.TotalJobs != 1 {
		t.Fatalf("summary jobs = %d, want only refreshed 30-day row", payload.Summary.TotalJobs)
	}
	if payload.Summary.Filters.Since != "2026-03-25" {
		t.Fatalf("summary since = %q, want default 30-day boundary", payload.Summary.Filters.Since)
	}
}

func TestTopLevelIngestCommandIsNotPublic(t *testing.T) {
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, cache: openTestCache(t), now: time.Now}
	defer app.cache.Close()

	err := app.Run(t.Context(), []string{"ingest", "actions"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "ingest"`) {
		t.Fatalf("ingest error = %v, want unknown command", err)
	}
}

func TestDoctorIngestActionsRunsManualRefresh(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	app := &App{
		stdout: &out,
		stderr: &bytes.Buffer{},
		cache:  cache,
		client: fakeActionsClient(),
		now:    func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
	}
	if err := app.Run(t.Context(), []string{"doctor", "ingest", "actions", "--account", "@me", "--repo", "octo/app", "--since", "2026-04-01", "--json"}); err != nil {
		t.Fatal(err)
	}

	var payload IngestResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("doctor ingest output is not JSON: %s", out.String())
	}
	if payload.RunsUpserted != 1 || payload.JobsUpserted != 1 {
		t.Fatalf("doctor ingest = %#v, want one run and one job", payload)
	}
}

func TestBillingRefreshAndSummaryDistinguishesDiscounts(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	app := &App{
		stdout: &out,
		stderr: &bytes.Buffer{},
		cache:  cache,
		client: fakeActionsClient(),
		now:    func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
	}
	if err := app.Run(t.Context(), []string{"billing", "refresh", "--account", "@me,demo-org", "--year", "2026", "--month", "4", "--json"}); err != nil {
		t.Fatal(err)
	}

	var refresh BillingRefreshResult
	if err := json.Unmarshal(out.Bytes(), &refresh); err != nil {
		t.Fatalf("billing refresh output is not JSON: %s", out.String())
	}
	if refresh.ItemsUpserted != 3 {
		t.Fatalf("billing refresh = %#v, want three items", refresh)
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"billing", "summary", "--group-by", "account,product,sku,cost-class", "--json"}); err != nil {
		t.Fatal(err)
	}
	var summary BillingSummary
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("billing summary output is not JSON: %s", out.String())
	}
	classes := map[string]bool{}
	for _, group := range summary.Groups {
		classes[group.Values["cost-class"]] = true
	}
	for _, want := range []string{"paid", "discounted", "free"} {
		if !classes[want] {
			t.Fatalf("billing classes = %#v, missing %s in %#v", classes, want, summary.Groups)
		}
	}
}

func TestServeRefreshRequiresAPIClient(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, cache: cache, now: time.Now}

	err := app.Run(t.Context(), []string{"serve", "--refresh", "--repo", "octo/app"})
	if err == nil || !strings.Contains(err.Error(), "GitHub API client unavailable") {
		t.Fatalf("serve refresh error = %v, want missing API client", err)
	}
}

func TestImportCommandIsIdempotent(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	payload := ExportPayload{
		ExportedAt: "2026-04-24T00:00:00Z",
		Repos:      []Repo{{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app", Private: true}},
		Runs:       []RunRecord{{ID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", RunStartedAt: "2026-04-24T10:00:00Z", Conclusion: "success"}},
		Jobs:       []JobRecord{{ID: 100, RunID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", Name: "test", StartedAt: "2026-04-24T10:00:00Z", CompletedAt: "2026-04-24T10:01:00Z", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest", OS: "Linux", Arch: "unknown", Type: "github-hosted"}, Conclusion: "success"}},
		BillingUsage: []BillingUsageRecord{
			{Key: "billing-1", Account: "octo", AccountKind: "user", Date: "2026-04-01", Product: "Actions", SKU: "actions_linux", CostClass: "paid", GrossAmount: 1, NetAmount: 1},
		},
	}
	path := filepath.Join(t.TempDir(), "export.json")
	writeFixtureExport(t, path, payload)

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: &bytes.Buffer{}, cache: cache, now: time.Now}
	for i := 0; i < 2; i++ {
		if err := app.Run(t.Context(), []string{"import", "--in", path, "--json"}); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := cache.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["repos"] != 1 || stats["runs"] != 1 || stats["jobs"] != 1 || stats["billing_usage"] != 1 {
		t.Fatalf("stats = %#v, want one row per table", stats)
	}
}

func TestExportCommandIncludesRepos(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	if err := cache.UpsertRepo(Repo{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app", Private: true}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertRun(RunRecord{ID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", RunStartedAt: "2026-04-24T10:00:00Z", Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertJob(JobRecord{ID: 100, RunID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", Name: "test", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertBillingUsage(BillingUsageRecord{Key: "billing-1", Account: "octo", AccountKind: "user", Date: "2026-04-01", Product: "Actions", SKU: "actions_linux", CostClass: "paid", GrossAmount: 1, NetAmount: 1}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "export.json")
	var out bytes.Buffer
	app := &App{stdout: &out, stderr: &bytes.Buffer{}, cache: cache, now: func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) }}
	if err := app.Run(t.Context(), []string{"export", "--out", path}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload ExportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Repos) != 1 || payload.Repos[0].FullName != "octo/app" {
		t.Fatalf("repos = %#v, want exported repo", payload.Repos)
	}
	if len(payload.BillingUsage) != 1 || payload.BillingUsage[0].Product != "Actions" {
		t.Fatalf("billing usage = %#v, want exported billing row", payload.BillingUsage)
	}
	if !strings.Contains(out.String(), "exported 1 repos, 1 runs, 1 jobs, and 1 billing rows") {
		t.Fatalf("export output = %q", out.String())
	}
}

func TestWebHandlerServesDashboardAndData(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	if err := cache.UpsertJob(JobRecord{ID: 1, RunID: 1, Repo: "octo/app", Name: "test", WorkflowPath: "ci.yml", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}

	app := &App{cache: cache, now: time.Now}
	handler, err := app.webHandler()
	if err != nil {
		t.Fatal(err)
	}

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "Flamegraph") {
		t.Fatalf("bad index response: %d %s", index.Code, index.Body.String())
	}

	jobs := httptest.NewRecorder()
	handler.ServeHTTP(jobs, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), "octo/app") {
		t.Fatalf("bad jobs response: %d %s", jobs.Code, jobs.Body.String())
	}
}

func TestWebHandlerCanScopeDashboardData(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	if err := cache.UpsertJob(JobRecord{ID: 1, RunID: 1, Repo: "octo/app", Name: "test", WorkflowPath: "ci.yml", StartedAt: "2026-04-24T10:00:00Z", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertJob(JobRecord{ID: 2, RunID: 2, Repo: "other/app", Name: "old", WorkflowPath: "ci.yml", StartedAt: "2026-04-24T10:00:00Z", DurationSecs: 300, Runner: RunnerMetadata{Image: "macos-15"}, Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}

	app := &App{cache: cache, now: time.Now}
	handler, err := app.webHandlerWithScope(WebScope{Repos: []Repo{{FullName: "octo/app"}}})
	if err != nil {
		t.Fatal(err)
	}

	summary := httptest.NewRecorder()
	handler.ServeHTTP(summary, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"total_jobs": 1`) || strings.Contains(summary.Body.String(), "other/app") {
		t.Fatalf("scoped summary response: %d %s", summary.Code, summary.Body.String())
	}

	jobs := httptest.NewRecorder()
	handler.ServeHTTP(jobs, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	if jobs.Code != http.StatusOK || strings.Contains(jobs.Body.String(), "other/app") {
		t.Fatalf("scoped jobs response: %d %s", jobs.Code, jobs.Body.String())
	}
}

func writeFixtureExport(t *testing.T, path string, payload ExportPayload) {
	t.Helper()
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func openTestCache(t *testing.T) *Cache {
	t.Helper()
	cache, err := OpenCache(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func fakeActionsClient() *fakeClient {
	return &fakeClient{responses: map[string]string{
		"user": `{"login":"octo"}`,
		"user/repos?visibility=all&affiliation=owner&sort=full_name": `[
			{"id":1,"name":"app","full_name":"octo/app","private":false,"owner":{"login":"octo","type":"User"}}
		]`,
		"orgs/demo-org/repos?type=all&sort=full_name": `[
			{"id":2,"name":"mobile","full_name":"demo-org/mobile","private":true,"owner":{"login":"demo-org","type":"Organization"}}
		]`,
		"repos/octo/app/actions/runs?per_page=100&created=%3E%3D2026-04-01": `{"workflow_runs":[
			{"id":10,"name":"CI","workflow_id":11,"run_number":1,"run_attempt":1,"event":"push","head_branch":"main","status":"completed","conclusion":"success","created_at":"2026-04-24T10:00:00Z","run_started_at":"2026-04-24T10:00:00Z","html_url":"https://github.com/octo/app/actions/runs/10","actor":{"login":"octocat"}}
		]}`,
		"repos/octo/app/actions/runs?per_page=100&created=%3E%3D2026-03-25": `{"workflow_runs":[
			{"id":10,"name":"CI","workflow_id":11,"run_number":1,"run_attempt":1,"event":"push","head_branch":"main","status":"completed","conclusion":"success","created_at":"2026-04-24T10:00:00Z","run_started_at":"2026-04-24T10:00:00Z","html_url":"https://github.com/octo/app/actions/runs/10","actor":{"login":"octocat"}}
		]}`,
		"repos/octo/app/actions/workflows/11": `{"path":".github/workflows/ci.yml"}`,
		"repos/octo/app/actions/runs/10/jobs?filter=all&per_page=100": `{"jobs":[
			{"id":100,"run_id":10,"name":"test","status":"completed","conclusion":"success","started_at":"2026-04-24T10:00:00Z","completed_at":"2026-04-24T10:02:00Z","html_url":"https://github.com/octo/app/actions/runs/10/job/100","runner_name":"GitHub Actions 1","runner_group_name":"GitHub Actions","labels":["ubuntu-latest"]}
		]}`,
		"repos/demo-org/mobile/actions/runs?per_page=100&created=%3E%3D2026-04-01": `{"workflow_runs":[
			{"id":20,"name":"Mobile CI","workflow_id":21,"run_number":8,"run_attempt":1,"event":"push","head_branch":"main","status":"completed","conclusion":"success","created_at":"2026-04-24T11:00:00Z","run_started_at":"2026-04-24T11:00:00Z","html_url":"https://github.com/demo-org/mobile/actions/runs/20","actor":{"login":"octocat"}}
		]}`,
		"repos/demo-org/mobile/actions/workflows/21": `{"path":".github/workflows/mobile.yml"}`,
		"repos/demo-org/mobile/actions/runs/20/jobs?filter=all&per_page=100": `{"jobs":[
			{"id":200,"run_id":20,"name":"ios","status":"completed","conclusion":"success","started_at":"2026-04-24T11:00:00Z","completed_at":"2026-04-24T11:05:00Z","html_url":"https://github.com/demo-org/mobile/actions/runs/20/job/200","runner_name":"GitHub Actions 2","runner_group_name":"GitHub Actions","labels":["macos-15","ARM64"]}
		]}`,
		"users/octo/settings/billing/usage?year=2026&month=4": `{"usageItems":[
			{"date":"2026-04-01","product":"Actions","sku":"actions_macos","quantity":10,"unitType":"minutes","pricePerUnit":0.08,"grossAmount":0.80,"discountAmount":0,"netAmount":0.80,"repositoryName":"octo/app"}
		]}`,
		"organizations/demo-org/settings/billing/usage?year=2026&month=4": `{"usageItems":[
			{"date":"2026-04-01","product":"Actions","sku":"actions_linux","quantity":100,"unitType":"minutes","pricePerUnit":0.008,"grossAmount":0.80,"discountAmount":0.80,"netAmount":0,"organizationName":"demo-org","repositoryName":"demo-org/mobile"},
			{"date":"2026-04-02","product":"Codespaces","sku":"codespaces_compute","quantity":1,"unitType":"hours","pricePerUnit":0,"grossAmount":0,"discountAmount":0,"netAmount":0,"organizationName":"demo-org"}
		]}`,
	}}
}
