package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type exactClient struct {
	responses map[string]string
	err       error
	requests  []string
}

func (c *exactClient) Get(path string, resp interface{}) error {
	c.requests = append(c.requests, path)
	if c.err != nil {
		return c.err
	}
	data, ok := c.responses[path]
	if !ok {
		return fmt.Errorf("missing fake response for %s", path)
	}
	return json.Unmarshal([]byte(data), resp)
}

func TestRunHelpRoutingAndUsageErrors(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}} {
		var out bytes.Buffer
		app := &App{stdout: &out, stderr: io.Discard, cache: cache, now: fixedNow}
		if err := app.Run(t.Context(), args); err != nil {
			t.Fatalf("Run(%v) error = %v", args, err)
		}
		if !strings.Contains(out.String(), "gh actions-usage: cached GitHub Actions") {
			t.Fatalf("help output for %v = %q", args, out.String())
		}
	}

	app := &App{stdout: io.Discard, stderr: io.Discard, cache: cache, now: fixedNow}
	errorCases := [][]string{
		{"nope"},
		{"accounts"},
		{"repos"},
		{"billing"},
		{"billing", "nope"},
		{"runs"},
		{"jobs"},
		{"api"},
		{"cache"},
		{"cache", "nope"},
	}
	for _, args := range errorCases {
		if err := app.Run(t.Context(), args); err == nil {
			t.Fatalf("Run(%v) succeeded, want usage error", args)
		}
	}
}

func TestRefreshActionsRespectsZeroDayWindow(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	client := &exactClient{responses: map[string]string{
		"user": `{"login":"octo"}`,
		"user/repos?visibility=all&affiliation=owner&sort=full_name&page=1&per_page=100": `[
			{"id":1,"name":"app","full_name":"octo/app","private":false,"owner":{"login":"octo","type":"User"}}
		]`,
		"repos/octo/app/actions/runs?per_page=100&created=%3E%3D2026-04-24&page=1&per_page=100": `{"workflow_runs":[]}`,
	}}
	app := &App{stdout: io.Discard, stderr: io.Discard, cache: cache, client: client, now: fixedNow}

	if _, _, err := app.refreshActions(t.Context(), IngestOptions{Account: "@me", Days: 0, DaysSet: true}); err != nil {
		t.Fatal(err)
	}
	for _, request := range client.requests {
		if strings.Contains(request, "created=%3E%3D2026-04-24") {
			return
		}
	}
	t.Fatalf("requests = %#v, want today created query", client.requests)
}

func TestFlagParseErrorsAreReturned(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	app := &App{stdout: io.Discard, stderr: io.Discard, cache: cache, client: fakeActionsClient(), now: fixedNow}

	commands := [][]string{
		{"doctor", "--bad"},
		{"accounts", "list", "--bad"},
		{"repos", "list", "--bad"},
		{"doctor", "ingest", "actions", "--bad"},
		{"report", "--bad"},
		{"billing", "refresh", "--bad"},
		{"billing", "summary", "--bad"},
		{"runs", "list", "--bad"},
		{"jobs", "list", "--bad"},
		{"summary", "--bad"},
		{"export", "--bad"},
		{"import", "--bad"},
		{"serve", "--bad"},
	}
	for _, args := range commands {
		if err := app.Run(t.Context(), args); err == nil {
			t.Fatalf("Run(%v) succeeded, want flag parse error", args)
		}
	}
}

func TestAuthSourceHonorsTokenPrecedence(t *testing.T) {
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	if got := authSource(); got != "env:GH_TOKEN" {
		t.Fatalf("authSource with GH_TOKEN = %q", got)
	}

	t.Setenv("GH_TOKEN", "")
	if got := authSource(); got != "env:GITHUB_TOKEN" {
		t.Fatalf("authSource with GITHUB_TOKEN = %q", got)
	}

	t.Setenv("GITHUB_TOKEN", "")
	if got := authSource(); got != "gh" {
		t.Fatalf("authSource without tokens = %q", got)
	}

	t.Setenv("GH_ENTERPRISE_TOKEN", "enterprise-token")
	if got := authSource(); got != "env:GH_ENTERPRISE_TOKEN" {
		t.Fatalf("authSource with GH_ENTERPRISE_TOKEN = %q", got)
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "enterprise-token")
	if got := authSource(); got != "env:GITHUB_ENTERPRISE_TOKEN" {
		t.Fatalf("authSource with GITHUB_ENTERPRISE_TOKEN = %q", got)
	}
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
}

func TestDoctorCommandReportsAPIAuthAndCacheState(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: io.Discard, cache: cache, client: fakeActionsClient(), now: fixedNow}
	if err := app.Run(t.Context(), []string{"doctor", "--json"}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("doctor output is not JSON: %s", out.String())
	}
	auth := payload["auth"].(map[string]any)
	if auth["ok"] != true || auth["login"] != "octo" {
		t.Fatalf("doctor auth = %#v", auth)
	}

	out.Reset()
	app = &App{stdout: &out, stderr: io.Discard, cache: cache, now: fixedNow}
	if err := app.Run(t.Context(), []string{"doctor"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auth: missing or invalid") {
		t.Fatalf("doctor text output = %q", out.String())
	}

	out.Reset()
	app = &App{stdout: &out, stderr: io.Discard, cache: cache, client: fakeActionsClient(), now: fixedNow}
	if err := app.Run(t.Context(), []string{"doctor"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auth: ok") {
		t.Fatalf("doctor auth-ok text output = %q", out.String())
	}

	out.Reset()
	app = &App{stdout: &out, stderr: io.Discard, cache: cache, client: &exactClient{err: errors.New("api down")}, now: fixedNow}
	if err := app.Run(t.Context(), []string{"doctor", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "api down") {
		t.Fatalf("doctor api-error output = %s", out.String())
	}

	closed := openTestCache(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	app = &App{stdout: &out, stderr: io.Discard, cache: closed, client: fakeActionsClient(), now: fixedNow}
	if err := app.Run(t.Context(), []string{"doctor", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"ok": false`) {
		t.Fatalf("closed-cache doctor output = %s", out.String())
	}
}

func TestAccountsAndReposCommandsUseGitHubClient(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	client := fakeActionsClient()
	client.responses["user/orgs?per_page=100"] = `[{"login":"demo-org"}]`
	var out bytes.Buffer
	app := &App{stdout: &out, stderr: io.Discard, cache: cache, client: client, now: fixedNow}

	if err := app.Run(t.Context(), []string{"accounts", "list", "--json"}); err != nil {
		t.Fatal(err)
	}
	var accounts []Account
	if err := json.Unmarshal(out.Bytes(), &accounts); err != nil {
		t.Fatalf("accounts output is not JSON: %s", out.String())
	}
	if len(accounts) != 2 || accounts[0].Login != "octo" || accounts[1].Login != "demo-org" {
		t.Fatalf("accounts = %#v", accounts)
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"accounts", "list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "org\tdemo-org") {
		t.Fatalf("accounts table = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"repos", "list", "--account", "@me", "--json"}); err != nil {
		t.Fatal(err)
	}
	var repos []Repo
	if err := json.Unmarshal(out.Bytes(), &repos); err != nil {
		t.Fatalf("repos output is not JSON: %s", out.String())
	}
	if len(repos) != 1 || repos[0].FullName != "octo/app" {
		t.Fatalf("personal repos = %#v", repos)
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"repos", "list", "--account", "demo-org"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "demo-org/mobile") {
		t.Fatalf("org repos table = %q", out.String())
	}

	app.client = nil
	if err := app.Run(t.Context(), []string{"accounts", "list"}); err == nil {
		t.Fatal("accounts list without client succeeded")
	}
	if _, err := app.fetchRepos(t.Context(), "@me"); err == nil {
		t.Fatal("fetchRepos without client succeeded")
	}

	app.client = &exactClient{err: errors.New("user lookup failed")}
	if err := app.Run(t.Context(), []string{"accounts", "list"}); err == nil {
		t.Fatal("accounts list accepted user lookup error")
	}
	if err := app.Run(t.Context(), []string{"repos", "list", "--account", "@me"}); err == nil {
		t.Fatal("repos list accepted user lookup error")
	}
}

func TestCachedListSummaryAndBillingCommands(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	seedCommandFixture(t, cache)

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: io.Discard, cache: cache, now: fixedNow}

	if err := app.Run(t.Context(), []string{"runs", "list", "--repo", "octo/app", "--limit", "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "octo/app") || !strings.Contains(out.String(), "ci.yml") {
		t.Fatalf("runs table = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"runs", "list", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"repo": "octo/app"`) {
		t.Fatalf("runs JSON = %s", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"jobs", "list", "--since", "2026-04-01", "--until", "2026-04-30"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1h1m") || !strings.Contains(out.String(), "1m5s") {
		t.Fatalf("jobs table = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"jobs", "list", "--repo", "demo-org/mobile", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"repo": "demo-org/mobile"`) {
		t.Fatalf("jobs JSON = %s", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"summary", "--group-by", "repo,workflow-path,runner-image"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "runtime:") || !strings.Contains(out.String(), "macos-15") {
		t.Fatalf("summary table = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"billing", "summary", "--group-by", "account,product,sku,cost-class"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "net: $") || !strings.Contains(out.String(), "Actions") {
		t.Fatalf("billing table = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"summary", "--group-by", ""}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "jobs: 2") {
		t.Fatalf("summary without groups = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"billing", "summary", "--group-by", ""}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "items: 2") {
		t.Fatalf("billing summary without groups = %q", out.String())
	}

	closed := openTestCache(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	app.cache = closed
	for _, args := range [][]string{
		{"runs", "list"},
		{"jobs", "list"},
		{"summary"},
		{"billing", "summary"},
		{"cache", "stats"},
	} {
		if err := app.Run(t.Context(), args); err == nil {
			t.Fatalf("Run(%v) succeeded on closed cache", args)
		}
	}
}

func TestCacheCommandsPathStatsAndClear(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	seedCommandFixture(t, cache)

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: io.Discard, cache: cache, now: fixedNow}

	if err := app.Run(t.Context(), []string{"cache", "path"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != cache.Path() {
		t.Fatalf("cache path output = %q, want %q", out.String(), cache.Path())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"cache", "stats"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"jobs": 2`) {
		t.Fatalf("cache stats = %s", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"cache", "clear", "--help"}); err != nil {
		t.Fatal(err)
	}
	stats, err := cache.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["jobs"] != 2 {
		t.Fatalf("stats after cache clear --help = %#v, want rows preserved", stats)
	}
	if !strings.Contains(out.String(), "usage: cache path|stats|clear") {
		t.Fatalf("cache clear help output = %q", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"cache", "clear"}); err != nil {
		t.Fatal(err)
	}
	stats, err = cache.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["repos"] != 0 || stats["runs"] != 0 || stats["jobs"] != 0 || stats["billing_usage"] != 0 {
		t.Fatalf("stats after clear = %#v", stats)
	}
}

func TestAPICommandRequiresClientAndPrintsJSON(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	app := &App{stdout: io.Discard, stderr: io.Discard, cache: cache, now: fixedNow}
	if err := app.Run(t.Context(), []string{"api", "get", "/user"}); err == nil {
		t.Fatal("api get without client succeeded")
	}

	var out bytes.Buffer
	app = &App{stdout: &out, stderr: io.Discard, cache: cache, client: fakeActionsClient(), now: fixedNow}
	if err := app.Run(t.Context(), []string{"api", "get", "/user"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"login": "octo"`) {
		t.Fatalf("api output = %s", out.String())
	}

	app.client = &exactClient{err: errors.New("api down")}
	if err := app.Run(t.Context(), []string{"api", "get", "/user"}); err == nil {
		t.Fatal("api get accepted client error")
	}
}

func TestReportIngestBillingImportAndExportHumanBranches(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var out bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &out, stderr: &stderr, cache: cache, client: fakeActionsClient(), now: fixedNow}

	if err := app.Run(t.Context(), []string{"doctor", "ingest"}); err == nil {
		t.Fatal("doctor ingest without actions succeeded")
	}
	if err := app.Run(t.Context(), []string{"doctor", "ingest", "actions", "--account", "@me", "--repo", "octo/app", "--since", "2026-04-01"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ingested 1 repos, 1 runs, 1 jobs") {
		t.Fatalf("doctor ingest output = %q", out.String())
	}

	out.Reset()
	stderr.Reset()
	if err := app.Run(t.Context(), []string{"report", "--account", "@me", "--repo", "octo/app", "--since", "2026-04-01"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "refreshed 1 repos, 1 runs, 1 jobs") || !strings.Contains(out.String(), "jobs: 1") {
		t.Fatalf("report stdout=%q stderr=%q", out.String(), stderr.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"billing", "refresh", "--account", "@me", "--year", "2026", "--month", "4"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ingested 1 billing items across 1 accounts") {
		t.Fatalf("billing refresh output = %q", out.String())
	}

	app.client = nil
	if err := app.Run(t.Context(), []string{"billing", "refresh", "--account", "@me"}); err == nil {
		t.Fatal("billing refresh without client succeeded")
	}
	app.client = fakeActionsClient()

	if err := app.Run(t.Context(), []string{"export"}); err == nil {
		t.Fatal("export without --out succeeded")
	}
	if err := app.Run(t.Context(), []string{"import"}); err == nil {
		t.Fatal("import without --in succeeded")
	}
	if err := app.Run(t.Context(), []string{"import", "--in", filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("import missing file succeeded")
	}
	badJSON := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badJSON, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(t.Context(), []string{"import", "--in", badJSON}); err == nil {
		t.Fatal("import bad JSON succeeded")
	}

	noBilling := filepath.Join(t.TempDir(), "no-billing.json")
	writeFixtureExport(t, noBilling, ExportPayload{
		Repos: []Repo{{ID: 9, Owner: "octo", Name: "lib", FullName: "octo/lib"}},
		Runs:  []RunRecord{{ID: 90, Repo: "octo/lib", WorkflowName: "CI", RunStartedAt: "2026-04-24T10:00:00Z"}},
		Jobs:  []JobRecord{{ID: 900, RunID: 90, Repo: "octo/lib", WorkflowName: "CI", Name: "test", DurationSecs: 1}},
	})
	out.Reset()
	if err := app.Run(t.Context(), []string{"import", "--in", noBilling}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "imported 1 repos, 1 runs, 1 jobs") {
		t.Fatalf("import output = %q", out.String())
	}

	out.Reset()
	exportPath := filepath.Join(t.TempDir(), "export.json")
	fresh := openTestCache(t)
	defer fresh.Close()
	if err := fresh.UpsertRepo(Repo{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app"}); err != nil {
		t.Fatal(err)
	}
	if err := fresh.UpsertRun(RunRecord{ID: 1, Repo: "octo/app", WorkflowName: "CI", RunStartedAt: "2026-04-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := fresh.UpsertJob(JobRecord{ID: 1, RunID: 1, Repo: "octo/app", WorkflowName: "CI", Name: "test", DurationSecs: 1}); err != nil {
		t.Fatal(err)
	}
	app.cache = fresh
	if err := app.Run(t.Context(), []string{"export", "--out", exportPath}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "exported 1 repos, 1 runs, and 1 jobs") {
		t.Fatalf("export output = %q", out.String())
	}
}

func TestImportExportAndRefreshPropagateCacheErrors(t *testing.T) {
	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	writeFixtureExport(t, payloadPath, ExportPayload{
		Repos:        []Repo{{ID: 1, Owner: "octo", Name: "app", FullName: "octo/app"}},
		Runs:         []RunRecord{{ID: 1, Repo: "octo/app"}},
		Jobs:         []JobRecord{{ID: 1, RunID: 1, Repo: "octo/app"}},
		BillingUsage: []BillingUsageRecord{{Key: "billing-1", Account: "octo"}},
	})

	closed := openTestCache(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	app := &App{stdout: io.Discard, stderr: io.Discard, cache: closed, client: fakeActionsClient(), now: fixedNow}
	if err := app.Run(t.Context(), []string{"import", "--in", payloadPath}); err == nil {
		t.Fatal("import succeeded on closed cache")
	}
	if err := app.Run(t.Context(), []string{"billing", "refresh", "--account", "@me", "--year", "2026", "--month", "4"}); err == nil {
		t.Fatal("billing refresh succeeded on closed cache")
	}
	if _, _, err := app.refreshActions(t.Context(), IngestOptions{Account: "@me", RepoFilter: "octo/app", Since: "2026-04-01"}); err == nil {
		t.Fatal("refreshActions succeeded on closed cache")
	}

	for _, table := range []string{"runs", "repos", "billing_usage"} {
		cache := openTestCache(t)
		seedCommandFixture(t, cache)
		if _, err := cache.db.Exec("drop table " + table); err != nil {
			t.Fatal(err)
		}
		app.cache = cache
		err := app.Run(t.Context(), []string{"export", "--out", filepath.Join(t.TempDir(), table+".json")})
		if err == nil {
			t.Fatalf("export succeeded with missing %s table", table)
		}
		if err := cache.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServeRefreshScopesDashboardBeforeListenError(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	var stderr bytes.Buffer
	app := &App{
		stdout: io.Discard,
		stderr: &stderr,
		cache:  cache,
		client: fakeActionsClient(),
		now:    fixedNow,
	}
	err := app.Run(t.Context(), []string{
		"serve",
		"--refresh",
		"--account", "@me",
		"--repo", "octo/app",
		"--since", "2026-04-01",
		"--listen", "127.0.0.1:-1",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("serve error = %v, want invalid listen address", err)
	}
	if !strings.Contains(stderr.String(), "refreshed 1 repos, 1 runs, 1 jobs") {
		t.Fatalf("serve stderr = %q", stderr.String())
	}

	err = app.Run(t.Context(), []string{"serve", "--listen", "127.0.0.1:-1"})
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("serve without refresh error = %v, want invalid listen address", err)
	}
}

func TestServeWithScopePrintsAddressOpensBrowserAndServes(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	oldServe := serveHTTPFunc
	oldBrowse := browseURLFunc
	defer func() {
		serveHTTPFunc = oldServe
		browseURLFunc = oldBrowse
	}()

	var served bool
	var opened string
	serveHTTPFunc = func(ln net.Listener, handler http.Handler) error {
		served = true
		return http.ErrServerClosed
	}
	browseURLFunc = func(addr string, stdout io.Writer, stderr io.Writer) error {
		opened = addr
		return errors.New("browser failures are ignored")
	}

	var out bytes.Buffer
	app := &App{stdout: &out, stderr: io.Discard, cache: cache, now: fixedNow}
	err := app.serveWithScope(WebScope{}, "127.0.0.1:0", true)
	if !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveWithScope error = %v", err)
	}
	if !served || opened == "" || !strings.Contains(out.String(), "serving http://") {
		t.Fatalf("served=%t opened=%q output=%q", served, opened, out.String())
	}
}

func TestRunMainCoversStartupAndErrorPaths(t *testing.T) {
	oldOpen := openCacheFunc
	oldClient := restClientFunc
	defer func() {
		openCacheFunc = oldOpen
		restClientFunc = oldClient
	}()

	t.Setenv("GH_ACTIONS_USAGE_CACHE", filepath.Join(t.TempDir(), "cache.db"))
	restClientFunc = func() (APIClient, error) {
		return fakeActionsClient(), nil
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := runMain(t.Context(), []string{"doctor", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("runMain doctor code = %d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"extension": "gh-actions-usage"`) {
		t.Fatalf("runMain output = %s", out.String())
	}

	openCacheFunc = func(path string) (*Cache, error) {
		return nil, errors.New("cache should not open for help")
	}
	out.Reset()
	errOut.Reset()
	if code := runMain(t.Context(), []string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("runMain help code = %d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "gh actions-usage: cached GitHub Actions") {
		t.Fatalf("runMain help output = %q", out.String())
	}
	openCacheFunc = oldOpen

	openCacheFunc = func(path string) (*Cache, error) {
		return nil, errors.New("cache should not open for subcommand help")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cache", "clear", "--help"}, "usage: cache path|stats|clear"},
		{[]string{"summary", "--help"}, "usage: summary"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runMain(t.Context(), tc.args, &out, &errOut); code != 0 {
			t.Fatalf("runMain %v code = %d stderr=%q", tc.args, code, errOut.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("runMain %v output = %q, want %q", tc.args, out.String(), tc.want)
		}
	}
	openCacheFunc = oldOpen

	restClientFunc = func() (APIClient, error) {
		return nil, errors.New("gh auth unavailable")
	}
	out.Reset()
	errOut.Reset()
	if code := runMain(t.Context(), []string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("runMain help without client code = %d", code)
	}

	out.Reset()
	errOut.Reset()
	if code := runMain(t.Context(), []string{"unknown"}, &out, &errOut); code != 1 {
		t.Fatalf("runMain unknown code = %d", code)
	}
	if !strings.Contains(errOut.String(), `unknown command "unknown"`) {
		t.Fatalf("runMain unknown stderr = %q", errOut.String())
	}

	openCacheFunc = func(path string) (*Cache, error) {
		return nil, errors.New("cache boom")
	}
	errOut.Reset()
	if code := runMain(t.Context(), []string{"doctor"}, io.Discard, &errOut); code != 1 {
		t.Fatalf("runMain cache error code = %d", code)
	}
	if !strings.Contains(errOut.String(), "open cache: cache boom") {
		t.Fatalf("runMain cache stderr = %q", errOut.String())
	}
}

func TestWebHandlerErrorsAndLimits(t *testing.T) {
	cache := openTestCache(t)
	seedCommandFixture(t, cache)
	app := &App{cache: cache, now: fixedNow}
	handler, err := app.webHandler()
	if err != nil {
		t.Fatal(err)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.Code)
	}

	limited := httptest.NewRecorder()
	handler.ServeHTTP(limited, httptest.NewRequest(http.MethodGet, "/api/jobs?limit=1", nil))
	if limited.Code != http.StatusOK {
		t.Fatalf("jobs status = %d", limited.Code)
	}
	if got := limited.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("jobs content-type = %q, want application/json", got)
	}
	var jobs []JobRecord
	if err := json.Unmarshal(limited.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("jobs output is not JSON: %s", limited.Body.String())
	}
	if len(jobs) != 1 {
		t.Fatalf("limited jobs = %d, want 1", len(jobs))
	}

	summaryOK := httptest.NewRecorder()
	handler.ServeHTTP(summaryOK, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if summaryOK.Code != http.StatusOK {
		t.Fatalf("summary status = %d", summaryOK.Code)
	}
	if got := summaryOK.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("summary content-type = %q, want application/json", got)
	}

	scoped, err := app.scopedJobs(WebScope{Repos: []Repo{{FullName: "octo/app"}, {FullName: "demo-org/mobile"}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 {
		t.Fatalf("scoped limited jobs = %d, want 1", len(scoped))
	}

	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	summary := httptest.NewRecorder()
	handler.ServeHTTP(summary, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if summary.Code != http.StatusInternalServerError {
		t.Fatalf("closed-cache summary status = %d", summary.Code)
	}
}

func TestParsingFormattingAndCostHelpers(t *testing.T) {
	raw := map[string]json.RawMessage{
		"text":   []byte(`"hello"`),
		"number": []byte(`42`),
		"float":  []byte(`3.5`),
		"quoted": []byte(`"2.25"`),
		"bad":    []byte(`{}`),
	}
	if got := rawString(raw, "missing", "number"); got != "42" {
		t.Fatalf("rawString number = %q", got)
	}
	if got := rawFloat(raw, "quoted"); got != 2.25 {
		t.Fatalf("rawFloat quoted = %v", got)
	}
	if got := rawFloat(raw, "bad", "missing"); got != 0 {
		t.Fatalf("rawFloat bad = %v", got)
	}

	year, month, day := dateParts("2026-04-24T10:00:00Z")
	if year != 2026 || month != 4 || day != 24 {
		t.Fatalf("dateParts = %d-%d-%d", year, month, day)
	}
	if year, month, day := dateParts("nope"); year != 0 || month != 0 || day != 0 {
		t.Fatalf("invalid dateParts = %d-%d-%d", year, month, day)
	}
	if year, month, day := dateParts(""); year != 0 || month != 0 || day != 0 {
		t.Fatalf("empty dateParts = %d-%d-%d", year, month, day)
	}

	durationCases := map[float64]string{
		0:    "0s",
		59:   "59s",
		65:   "1m5s",
		3661: "1h1m",
	}
	for seconds, want := range durationCases {
		if got := formatDuration(seconds); got != want {
			t.Fatalf("formatDuration(%v) = %q, want %q", seconds, got, want)
		}
	}
	if got := durationSeconds("", "2026-04-24T10:00:00Z"); got != 0 {
		t.Fatalf("durationSeconds missing start = %v", got)
	}
	if got := durationSeconds("bad", "2026-04-24T10:00:00Z"); got != 0 {
		t.Fatalf("durationSeconds bad start = %v", got)
	}
	if got := durationSeconds("2026-04-24T10:00:01Z", "2026-04-24T10:00:00Z"); got != 0 {
		t.Fatalf("durationSeconds negative = %v", got)
	}

	costCases := []struct {
		record BillingUsageRecord
		want   string
	}{
		{BillingUsageRecord{NetAmount: 1, DiscountAmount: 1}, "discounted"},
		{BillingUsageRecord{NetAmount: 1}, "paid"},
		{BillingUsageRecord{DiscountAmount: 1}, "discounted"},
		{BillingUsageRecord{}, "free"},
		{BillingUsageRecord{GrossAmount: 1}, "unknown"},
	}
	for _, tc := range costCases {
		if got := billingCostClass(tc.record); got != tc.want {
			t.Fatalf("billingCostClass(%#v) = %q, want %q", tc.record, got, tc.want)
		}
	}

	privateRepo := Repo{Private: true}
	classCases := []struct {
		repo Repo
		job  JobRecord
		want string
	}{
		{Repo{Private: true}, JobRecord{Runner: RunnerMetadata{Type: "self-hosted"}}, "external"},
		{Repo{Private: false}, JobRecord{}, "free"},
		{Repo{Private: true, BillingOwnerKind: "enterprise"}, JobRecord{}, "enterprise"},
		{Repo{Private: true, BillingPlan: "Enterprise Cloud"}, JobRecord{}, "enterprise"},
		{Repo{Private: true, BillingPlan: "pro"}, JobRecord{}, "paid"},
		{privateRepo, JobRecord{}, "unknown"},
	}
	for _, tc := range classCases {
		if got := actionCostClass(tc.repo, tc.job); got != tc.want {
			t.Fatalf("actionCostClass(%#v, %#v) = %q, want %q", tc.repo, tc.job, got, tc.want)
		}
	}

	if got := dateOnly("short"); got != "unknown" {
		t.Fatalf("dateOnly short = %q", got)
	}
	if got := repoPath("octo/app name"); got != "octo/app%20name" {
		t.Fatalf("repoPath escaped = %q", got)
	}
	if got := repoPath("octo app"); got != "octo%20app" {
		t.Fatalf("repoPath single part = %q", got)
	}
	if got := ownerFromRepo("single"); got != "" {
		t.Fatalf("ownerFromRepo single = %q", got)
	}
	if got := firstNonEmpty("", "  ", "value"); got != "value" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if !isNotFound(sql.ErrNoRows) || isNotFound(errors.New("nope")) {
		t.Fatal("isNotFound returned unexpected result")
	}
}

func TestSummariesHandleNoGroupsAndTieBreaks(t *testing.T) {
	if groups := summarize(nil, nil); groups != nil {
		t.Fatalf("summarize without groupBy = %#v", groups)
	}
	if groups := summarizeBilling(nil, nil); groups != nil {
		t.Fatalf("summarizeBilling without groupBy = %#v", groups)
	}

	jobs := []JobRecord{
		{Repo: "b/repo", RunID: 1, DurationSecs: 60, Conclusion: "success"},
		{Repo: "a/repo", RunID: 2, DurationSecs: 60, Conclusion: "success"},
	}
	groups := summarize(jobs, []string{"repo"})
	if len(groups) != 2 || groups[0].Values["repo"] != "a/repo" {
		t.Fatalf("summary tie groups = %#v", groups)
	}

	jobs = []JobRecord{
		{Repo: "octo/app", RunID: 1, DurationSecs: 60, Conclusion: "success"},
		{Repo: "octo/app", RunID: 1, DurationSecs: 30, Conclusion: "success"},
		{Repo: "octo/app", RunID: 2, DurationSecs: 10, Conclusion: "failure"},
	}
	groups = summarize(jobs, []string{"repo"})
	if len(groups) != 1 || groups[0].Runs != 2 {
		t.Fatalf("summary runs = %#v, want two distinct runs", groups)
	}
}

func TestValidationFallbackAndRunnerBranches(t *testing.T) {
	var repo Repo
	if err := json.Unmarshal([]byte(`{`), &repo); err == nil {
		t.Fatal("Repo accepted invalid JSON")
	}
	if err := json.Unmarshal([]byte(`{"id":1,"owner":"octo","name":"app"}`), &repo); err != nil {
		t.Fatal(err)
	}
	if repo.FullName != "octo/app" {
		t.Fatalf("repo full_name fallback = %q", repo.FullName)
	}

	var run WorkflowRunAPI
	capture := rawCapture[WorkflowRunAPI]{target: &run}
	if err := capture.UnmarshalJSON([]byte(`{"id":"bad"}`)); err == nil {
		t.Fatal("rawCapture accepted invalid run")
	}

	if _, err := createdQuery("bad", "", 30, fixedNow()); err == nil {
		t.Fatal("createdQuery accepted invalid since")
	}
	if _, err := createdQuery("", "bad", 30, fixedNow()); err == nil {
		t.Fatal("createdQuery accepted invalid until")
	}

	if got := reportFilters("", "", "", 0, fixedNow()); got.Since != "2026-04-24" {
		t.Fatalf("reportFilters today = %#v", got)
	}
	if got := filterRepos(nil, "octo/app"); got != nil {
		t.Fatalf("filterRepos empty = %#v", got)
	}
	if got := filterJobsByRepos([]JobRecord{{Repo: "octo/app"}}, nil); got != nil {
		t.Fatalf("filterJobsByRepos empty selection = %#v", got)
	}

	if got := inferRepoOwnerKind(Repo{Owner: "other"}, accountContext{Kind: "org", Login: "demo-org"}); got != "" {
		t.Fatalf("inferRepoOwnerKind mismatch = %q", got)
	}
	meta := runnerMetadata([]string{"windows-2025", "x86_64", "arm"})
	if meta.OS != "Windows" || meta.Arch != "ARM" || meta.Image != "windows-2025" {
		t.Fatalf("runner metadata = %#v", meta)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Fatalf("all-empty firstNonEmpty = %q", got)
	}
}

func TestAccountSelectorsFlagsAndOverrides(t *testing.T) {
	selectorCases := map[string][2]string{
		"":                {"user", "@me"},
		"@me":             {"user", "@me"},
		"org/demo-org":    {"org", "demo-org"},
		"enterprise:acme": {"enterprise", "acme"},
		"personal:tiki":   {"user", "tiki"},
		"business/acme":   {"enterprise", "acme"},
		"demo-org":        {"org", "demo-org"},
	}
	for input, want := range selectorCases {
		kind, login := parseAccountSelector(input)
		if kind != want[0] || login != want[1] {
			t.Fatalf("parseAccountSelector(%q) = %q/%q, want %q/%q", input, kind, login, want[0], want[1])
		}
	}

	var flag keyValueFlag
	if got := flag.String(); got != "" {
		t.Fatalf("empty flag String = %q", got)
	}
	if err := flag.Set("b=2,a=1"); err != nil {
		t.Fatal(err)
	}
	if got := flag.String(); got != "a=1,b=2" {
		t.Fatalf("flag String = %q", got)
	}
	if err := flag.Set("broken"); err == nil {
		t.Fatal("keyValueFlag accepted malformed value")
	}

	if got := lookupOverride(map[string]string{"repo": "pro"}, "", "repo"); got != "pro" {
		t.Fatalf("lookupOverride = %q", got)
	}
	if got := lookupOverride(map[string]string{"repo": ""}, "repo", "missing"); got != "" {
		t.Fatalf("lookupOverride empty value = %q", got)
	}
}

func TestActionAndBillingAccountContexts(t *testing.T) {
	app := &App{stdout: io.Discard, stderr: io.Discard, cache: openTestCache(t), client: fakeActionsClient(), now: fixedNow}
	defer app.cache.Close()

	defaultActions, err := app.actionAccountContexts(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultActions) != 1 || defaultActions[0].Login != "octo" {
		t.Fatalf("default action accounts = %#v", defaultActions)
	}

	actions, err := app.actionAccountContexts(t.Context(), "@me,demo-org")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[0].Login != "octo" || actions[1].Kind != "org" {
		t.Fatalf("action accounts = %#v", actions)
	}
	if _, err := app.actionAccountContexts(t.Context(), "user:someone"); err == nil {
		t.Fatal("actionAccountContexts accepted explicit user")
	}
	if _, err := app.actionAccountContexts(t.Context(), "enterprise:acme"); err == nil {
		t.Fatal("actionAccountContexts accepted enterprise")
	}

	billing, err := app.billingAccountContexts(t.Context(), "@me,enterprise:acme,org/demo-org")
	if err != nil {
		t.Fatal(err)
	}
	if len(billing) != 3 || billing[0].Login != "octo" || billing[1].Kind != "enterprise" || billing[2].Login != "demo-org" {
		t.Fatalf("billing accounts = %#v", billing)
	}
	defaultBilling, err := app.billingAccountContexts(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultBilling) != 1 || defaultBilling[0].Login != "octo" {
		t.Fatalf("default billing accounts = %#v", defaultBilling)
	}

	emptyUser := &fakeClient{responses: map[string]string{"user": `{}`}}
	app.client = emptyUser
	if _, err := app.currentUserLogin(); err == nil {
		t.Fatal("currentUserLogin accepted empty login")
	}
	app.client = &exactClient{err: errors.New("api down")}
	if _, err := app.currentUserLogin(); err == nil {
		t.Fatal("currentUserLogin accepted API error")
	}
}

func TestDimensionVariants(t *testing.T) {
	job := JobRecord{
		Account:          "octo",
		Repo:             "octo/app",
		RepoOwner:        "octo",
		RepoOwnerKind:    "user",
		BillingOwner:     "billing-octo",
		BillingOwnerKind: "enterprise",
		BillingPlan:      "pro",
		CostClass:        "paid",
		WorkflowName:     "CI",
		WorkflowPath:     ".github/workflows/ci.yml",
		Name:             "test",
		RunnerName:       "runner-1",
		RunnerGroup:      "GitHub Actions",
		Runner:           RunnerMetadata{Type: "github-hosted", OS: "Linux", Arch: "X64", Image: "ubuntu-latest"},
		Conclusion:       "success",
		StartedAt:        "2026-04-24T10:00:00Z",
	}
	cases := map[string]string{
		"account":            "octo",
		"date":               "2026-04-24",
		"repo":               "octo/app",
		"repo-owner":         "octo",
		"repo-owner-kind":    "user",
		"billing-owner":      "billing-octo",
		"billing-owner-kind": "enterprise",
		"billing-plan":       "pro",
		"plan":               "pro",
		"cost-class":         "paid",
		"workflow":           "CI",
		"workflow-name":      "CI",
		"workflow-path":      ".github/workflows/ci.yml",
		"job":                "test",
		"runner":             "runner-1",
		"runner-name":        "runner-1",
		"runner-group":       "GitHub Actions",
		"runner-type":        "github-hosted",
		"runner-os":          "Linux",
		"os":                 "Linux",
		"runner-arch":        "X64",
		"arch":               "X64",
		"runner-image":       "ubuntu-latest",
		"image":              "ubuntu-latest",
		"platform":           "Linux/X64",
		"conclusion":         "success",
		"unknown-dim":        "unknown",
	}
	for dim, want := range cases {
		if got := dimension(job, dim); got != want {
			t.Fatalf("dimension(%q) = %q, want %q", dim, got, want)
		}
	}

	fallback := JobRecord{Repo: "octo/app", WorkflowName: "CI", Runner: RunnerMetadata{OS: "macOS"}}
	if got := dimension(fallback, "repo-owner"); got != "octo" {
		t.Fatalf("repo owner fallback = %q", got)
	}
	if got := dimension(fallback, "billing-owner"); got != "octo" {
		t.Fatalf("billing owner fallback = %q", got)
	}
	if got := dimension(fallback, "workflow-path"); got != "CI" {
		t.Fatalf("workflow path fallback = %q", got)
	}
	if got := dimension(fallback, "runner-image"); got != "unknown" {
		t.Fatalf("runner image fallback = %q", got)
	}
}

func TestBillingDimensionVariants(t *testing.T) {
	record := BillingUsageRecord{
		Account:          "octo",
		AccountKind:      "user",
		Date:             "2026-04-24",
		Year:             2026,
		Month:            4,
		Product:          "Actions",
		SKU:              "actions_macos",
		UnitType:         "minutes",
		Model:            "hosted",
		OrganizationName: "demo-org",
		RepositoryName:   "octo/app",
		CostCenterID:     "cc-1",
		CostClass:        "paid",
	}
	cases := map[string]string{
		"account":        "octo",
		"account-kind":   "user",
		"date":           "2026-04-24",
		"year":           "2026",
		"month":          "2026-04",
		"product":        "Actions",
		"sku":            "actions_macos",
		"unit":           "minutes",
		"unit-type":      "minutes",
		"model":          "hosted",
		"org":            "demo-org",
		"organization":   "demo-org",
		"repo":           "octo/app",
		"repository":     "octo/app",
		"cost-center":    "cc-1",
		"cost-center-id": "cc-1",
		"cost-class":     "paid",
		"unknown-dim":    "unknown",
	}
	for dim, want := range cases {
		if got := billingDimension(record, dim); got != want {
			t.Fatalf("billingDimension(%q) = %q, want %q", dim, got, want)
		}
	}
	if got := billingDimension(BillingUsageRecord{}, "year"); got != "unknown" {
		t.Fatalf("zero year = %q", got)
	}
	if got := billingDimension(BillingUsageRecord{}, "month"); got != "unknown" {
		t.Fatalf("zero month = %q", got)
	}
}

func TestSqlcBackedFilters(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()

	jobs := []JobRecord{
		{ID: 1, RunID: 1, Repo: "octo/app", Name: "kept", StartedAt: "2026-04-15T10:00:00Z", DurationSecs: 60},
		{ID: 2, RunID: 2, Repo: "octo/app", Name: "outside date", StartedAt: "2026-05-01T10:00:00Z", DurationSecs: 120},
		{ID: 3, RunID: 3, Repo: "other/app", Name: "outside repo", StartedAt: "2026-04-20T10:00:00Z", DurationSecs: 180},
	}
	for _, job := range jobs {
		if err := cache.UpsertJob(job); err != nil {
			t.Fatal(err)
		}
	}
	filteredJobs, err := cache.ListJobs(QueryFilters{Repo: "octo/app", Since: "2026-04-01", Until: "2026-04-30"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredJobs) != 1 || filteredJobs[0].ID != 1 {
		t.Fatalf("filtered jobs = %#v", filteredJobs)
	}

	runs := []RunRecord{
		{ID: 1, Repo: "octo/app", WorkflowName: "kept", RunStartedAt: "2026-04-15T10:00:00Z"},
		{ID: 2, Repo: "octo/app", WorkflowName: "outside date", RunStartedAt: "2026-05-01T10:00:00Z"},
		{ID: 3, Repo: "other/app", WorkflowName: "outside repo", RunStartedAt: "2026-04-20T10:00:00Z"},
	}
	for _, run := range runs {
		if err := cache.UpsertRun(run); err != nil {
			t.Fatal(err)
		}
	}
	filteredRuns, err := cache.ListRuns(QueryFilters{Repo: "octo/app", Since: "2026-04-01", Until: "2026-04-30"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredRuns) != 1 || filteredRuns[0].ID != 1 {
		t.Fatalf("filtered runs = %#v", filteredRuns)
	}

	billingFilters := BillingQueryFilters{
		Account:      "octo",
		Repo:         "octo/app",
		Since:        "2026-04-01",
		Until:        "2026-04-30",
		Year:         2026,
		Month:        4,
		Day:          24,
		Product:      "Actions",
		SKU:          "actions_macos",
		Organization: "demo-org",
		CostCenterID: "cc-1",
	}
	records := []BillingUsageRecord{
		{Key: "kept", Account: "octo", AccountKind: "user", Date: "2026-04-24", Year: 2026, Month: 4, Day: 24, Product: "Actions", SKU: "actions_macos", OrganizationName: "demo-org", RepositoryName: "octo/app", CostCenterID: "cc-1", NetAmount: 10},
		{Key: "outside-account", Account: "other", AccountKind: "user", Date: "2026-04-24", Year: 2026, Month: 4, Day: 24, Product: "Actions", SKU: "actions_macos", OrganizationName: "demo-org", RepositoryName: "octo/app", CostCenterID: "cc-1", NetAmount: 20},
		{Key: "outside-repo", Account: "octo", AccountKind: "user", Date: "2026-04-24", Year: 2026, Month: 4, Day: 24, Product: "Actions", SKU: "actions_macos", OrganizationName: "demo-org", RepositoryName: "other/app", CostCenterID: "cc-1", NetAmount: 30},
	}
	for _, record := range records {
		if err := cache.UpsertBillingUsage(record); err != nil {
			t.Fatal(err)
		}
	}
	filteredBilling, err := cache.ListBillingUsage(billingFilters)
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredBilling) != 1 || filteredBilling[0].Key != "kept" {
		t.Fatalf("filtered billing = %#v", filteredBilling)
	}
}

func TestPaginationAndFetchHelpers(t *testing.T) {
	repoItems := makeJSONItems(100, func(i int) string {
		return fmt.Sprintf(`{"id":%d,"name":"repo-%d","full_name":"octo/repo-%d","private":false,"owner":"octo"}`, i, i, i)
	})
	client := &exactClient{responses: map[string]string{
		"repos?page=1&per_page=100": repoItems,
		"repos?page=2&per_page=100": `[{"id":101,"name":"repo-101","full_name":"octo/repo-101","private":false,"owner":"octo"}]`,
	}}
	repos, err := fetchPaged[Repo](client, "repos")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 101 || client.requests[1] != "repos?page=2&per_page=100" {
		t.Fatalf("paged repos len=%d requests=%#v", len(repos), client.requests)
	}

	jobItems := makeJSONItems(100, func(i int) string {
		return fmt.Sprintf(`{"id":%d,"run_id":10,"name":"job-%d","status":"completed","started_at":"2026-04-24T10:00:00Z","completed_at":"2026-04-24T10:00:01Z","labels":["ubuntu-latest"]}`, i, i)
	})
	client = &exactClient{responses: map[string]string{
		"jobs?page=1&per_page=100": `{"jobs":` + jobItems + `}`,
		"jobs?page=2&per_page=100": `{"jobs":[{"id":101,"run_id":10,"name":"last","status":"queued","labels":[]}]}`,
	}}
	jobs, err := fetchPagedEnvelope[WorkflowJobAPI](client, "jobs", "jobs")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 101 || len(jobs[0].raw) == 0 {
		t.Fatalf("paged jobs len=%d raw=%q", len(jobs), string(jobs[0].raw))
	}

	client = &exactClient{err: errors.New("api down")}
	if _, err := fetchPaged[Repo](client, "repos"); err == nil {
		t.Fatal("fetchPaged accepted client error")
	}
	if _, err := fetchPagedEnvelope[WorkflowJobAPI](client, "jobs", "jobs"); err == nil {
		t.Fatal("fetchPagedEnvelope accepted client error")
	}

	client = &exactClient{responses: map[string]string{
		"jobs?page=1&per_page=100": `{"jobs":{}}`,
	}}
	if _, err := fetchPagedEnvelope[WorkflowJobAPI](client, "jobs", "jobs"); err == nil {
		t.Fatal("fetchPagedEnvelope accepted non-array envelope")
	}

	client = &exactClient{responses: map[string]string{
		"jobs?page=1&per_page=100": `{"jobs":[{"id":"bad"}]}`,
	}}
	if _, err := fetchPagedEnvelope[WorkflowJobAPI](client, "jobs", "jobs"); err == nil {
		t.Fatal("fetchPagedEnvelope accepted malformed item")
	}
}

func TestFetchRepoRunJobAndBillingEdgeCases(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	app := &App{stdout: io.Discard, stderr: io.Discard, cache: cache, client: fakeActionsClient(), now: fixedNow}

	result, selected, err := app.refreshActions(t.Context(), IngestOptions{RepoFilter: "octo/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReposIngested != 1 || len(selected) != 1 {
		t.Fatalf("default refresh result=%#v selected=%#v", result, selected)
	}
	if _, err := app.fetchReposForAccount(t.Context(), accountContext{Kind: "enterprise", Login: "acme"}); err == nil {
		t.Fatal("fetchReposForAccount accepted enterprise")
	}
	if path := app.fetchWorkflowPath(Repo{FullName: "octo/missing"}, 404); path != "" {
		t.Fatalf("missing workflow path = %q", path)
	}
	if _, _, err := app.refreshActions(t.Context(), IngestOptions{Account: "@me", RepoFilter: "octo/missing", Since: "2026-04-01"}); err == nil {
		t.Fatal("refreshActions accepted empty repository selection")
	}
	if _, err := billingUsageEndpoint(accountContext{Kind: "team", Login: "demo"}, BillingQueryFilters{}); err == nil {
		t.Fatal("billingUsageEndpoint accepted unsupported kind")
	}
	if endpoint, err := billingUsageEndpoint(accountContext{Kind: "user", Login: "octo"}, BillingQueryFilters{}); err != nil || endpoint != "users/octo/settings/billing/usage" {
		t.Fatalf("billing endpoint = %q err=%v", endpoint, err)
	}
	app.client = &exactClient{err: errors.New("billing api down")}
	if _, err := app.fetchBillingUsage(t.Context(), accountContext{Kind: "user", Login: "octo"}, BillingQueryFilters{}); err == nil {
		t.Fatal("fetchBillingUsage accepted client error")
	}

	record := parseBillingUsageItem(json.RawMessage(`{
		"date":"2026-04-24",
		"productName":"Actions",
		"skuName":"actions_linux",
		"unit_type":"minutes",
		"quantity":"3.5",
		"gross_amount":"1.40",
		"discount_amount":"0.40",
		"net_amount":"1.00",
		"organization":"demo-org",
		"repository":"demo-org/mobile",
		"cost_center_id":17
	}`), accountContext{Kind: "org", Login: "demo-org"}, BillingQueryFilters{})
	if record.Year != 2026 || record.Month != 4 || record.Day != 24 || record.CostCenterID != "17" || record.CostClass != "discounted" {
		t.Fatalf("parsed billing record = %#v", record)
	}
}

func TestOpenCacheAndClosedCacheErrorPaths(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCache(filepath.Join(blocker, "cache.db")); err == nil {
		t.Fatal("OpenCache succeeded under a file path")
	}

	cache := openTestCache(t)
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Clear(); err == nil {
		t.Fatal("Clear succeeded on closed cache")
	}
	if _, err := cache.ListRepos(); err == nil {
		t.Fatal("ListRepos succeeded on closed cache")
	}
	if _, err := cache.ListRuns(QueryFilters{}); err == nil {
		t.Fatal("ListRuns succeeded on closed cache")
	}
	if _, err := cache.ListJobs(QueryFilters{}); err == nil {
		t.Fatal("ListJobs succeeded on closed cache")
	}
	if _, err := cache.ListBillingUsage(BillingQueryFilters{}); err == nil {
		t.Fatal("ListBillingUsage succeeded on closed cache")
	}
	if err := cache.UpsertRepo(Repo{FullName: "octo/app"}); err == nil {
		t.Fatal("UpsertRepo succeeded on closed cache")
	}
	if err := cache.UpsertRun(RunRecord{ID: 1}); err == nil {
		t.Fatal("UpsertRun succeeded on closed cache")
	}
	if err := cache.UpsertJob(JobRecord{ID: 1}); err == nil {
		t.Fatal("UpsertJob succeeded on closed cache")
	}
	if err := cache.UpsertBillingUsage(BillingUsageRecord{Key: "k"}); err == nil {
		t.Fatal("UpsertBillingUsage succeeded on closed cache")
	}
}

func TestCacheBillingFiltersUseWhereAndLimit(t *testing.T) {
	cache := openTestCache(t)
	defer cache.Close()
	seedCommandFixture(t, cache)

	records, err := cache.ListBillingUsage(BillingQueryFilters{
		Account:      "demo-org",
		Repo:         "demo-org/mobile",
		Since:        "2026-04-01",
		Until:        "2026-04-30",
		Product:      "Actions",
		SKU:          "actions_macos",
		Organization: "demo-org",
		Limit:        1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Account != "demo-org" {
		t.Fatalf("filtered billing records = %#v", records)
	}
}

func seedCommandFixture(t *testing.T, cache *Cache) {
	t.Helper()
	repos := []Repo{
		{ID: 1, Account: "octo", Owner: "octo", OwnerKind: "user", Name: "app", FullName: "octo/app", BillingOwner: "octo", BillingOwnerKind: "user", BillingPlan: "pro"},
		{ID: 2, Account: "demo-org", Owner: "demo-org", OwnerKind: "org", Name: "mobile", FullName: "demo-org/mobile", Private: true, BillingOwner: "acme", BillingOwnerKind: "enterprise", BillingPlan: "enterprise"},
	}
	for _, repo := range repos {
		if err := cache.UpsertRepo(repo); err != nil {
			t.Fatal(err)
		}
	}
	runs := []RunRecord{
		{ID: 10, Account: "octo", Repo: "octo/app", WorkflowName: "CI", WorkflowPath: "ci.yml", RunStartedAt: "2026-04-24T10:00:00Z", Conclusion: "success", HTMLURL: "https://example.com/10"},
		{ID: 20, Account: "demo-org", Repo: "demo-org/mobile", WorkflowName: "Mobile", WorkflowPath: "mobile.yml", RunStartedAt: "2026-04-24T11:00:00Z", Conclusion: "failure", HTMLURL: "https://example.com/20"},
	}
	for _, run := range runs {
		if err := cache.UpsertRun(run); err != nil {
			t.Fatal(err)
		}
	}
	jobs := []JobRecord{
		{ID: 100, RunID: 10, Account: "octo", Repo: "octo/app", RepoOwner: "octo", RepoOwnerKind: "user", BillingOwner: "octo", BillingOwnerKind: "user", BillingPlan: "pro", CostClass: "paid", WorkflowName: "CI", WorkflowPath: "ci.yml", Name: "test", Status: "completed", Conclusion: "success", StartedAt: "2026-04-24T10:00:00Z", CompletedAt: "2026-04-24T10:01:05Z", DurationSecs: 65, RunnerName: "runner-1", RunnerGroup: "GitHub Actions", Runner: RunnerMetadata{Type: "github-hosted", OS: "Linux", Arch: "X64", Image: "ubuntu-latest"}, Labels: []string{"ubuntu-latest"}},
		{ID: 200, RunID: 20, Account: "demo-org", Repo: "demo-org/mobile", RepoOwner: "demo-org", RepoOwnerKind: "org", BillingOwner: "acme", BillingOwnerKind: "enterprise", BillingPlan: "enterprise", CostClass: "enterprise", WorkflowName: "Mobile", WorkflowPath: "mobile.yml", Name: "ios", Status: "completed", Conclusion: "failure", StartedAt: "2026-04-24T11:00:00Z", CompletedAt: "2026-04-24T12:01:01Z", DurationSecs: 3661, RunnerName: "runner-2", RunnerGroup: "GitHub Actions", Runner: RunnerMetadata{Type: "github-hosted", OS: "macOS", Arch: "ARM64", Image: "macos-15"}, Labels: []string{"macos-15", "ARM64"}},
	}
	for _, job := range jobs {
		if err := cache.UpsertJob(job); err != nil {
			t.Fatal(err)
		}
	}
	billing := []BillingUsageRecord{
		{Key: "billing-1", Account: "octo", AccountKind: "user", Date: "2026-04-24", Year: 2026, Month: 4, Day: 24, Product: "Actions", SKU: "actions_linux", UnitType: "minutes", RepositoryName: "octo/app", CostClass: "paid", GrossQuantity: 10, NetQuantity: 10, GrossAmount: 0.80, NetAmount: 0.80},
		{Key: "billing-2", Account: "demo-org", AccountKind: "org", Date: "2026-04-24", Year: 2026, Month: 4, Day: 24, Product: "Actions", SKU: "actions_macos", UnitType: "minutes", OrganizationName: "demo-org", RepositoryName: "demo-org/mobile", CostClass: "discounted", GrossQuantity: 10, DiscountQuantity: 5, NetQuantity: 5, GrossAmount: 0.80, DiscountAmount: 0.40, NetAmount: 0.40},
	}
	for _, record := range billing {
		if err := cache.UpsertBillingUsage(record); err != nil {
			t.Fatal(err)
		}
	}
}

func makeJSONItems(count int, item func(int) string) string {
	parts := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		parts = append(parts, item(i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func fixedNow() time.Time {
	return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
}
