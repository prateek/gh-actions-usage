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

func TestImportCommandIsIdempotent(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	payload := ExportPayload{
		ExportedAt: "2026-04-24T00:00:00Z",
		Repos:      []Repo{{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app", Private: true}},
		Runs:       []RunRecord{{ID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", RunStartedAt: "2026-04-24T10:00:00Z", Conclusion: "success"}},
		Jobs:       []JobRecord{{ID: 100, RunID: 10, Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", Name: "test", StartedAt: "2026-04-24T10:00:00Z", CompletedAt: "2026-04-24T10:01:00Z", DurationSecs: 60, Runner: RunnerMetadata{Image: "ubuntu-latest", OS: "Linux", Arch: "unknown", Type: "github-hosted"}, Conclusion: "success"}},
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
	if stats["repos"] != 1 || stats["runs"] != 1 || stats["jobs"] != 1 {
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
	if !strings.Contains(out.String(), "exported 1 repos, 1 runs, and 1 jobs") {
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
