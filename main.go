package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/browser"
	_ "modernc.org/sqlite"
)

const (
	appName    = "gh-actions-usage"
	dateFormat = "2006-01-02"
)

//go:embed web/index.html
var webIndexHTML string

type APIClient interface {
	Get(path string, resp interface{}) error
}

type App struct {
	stdout io.Writer
	stderr io.Writer
	client APIClient
	cache  *Cache
	now    func() time.Time
}

type RunnerMetadata struct {
	Type  string `json:"type"`
	OS    string `json:"os"`
	Arch  string `json:"arch"`
	Image string `json:"image"`
}

type Account struct {
	Kind  string `json:"kind"`
	Login string `json:"login"`
}

type Repo struct {
	ID       int64           `json:"id"`
	Owner    string          `json:"owner"`
	Name     string          `json:"name"`
	FullName string          `json:"full_name"`
	Private  bool            `json:"private"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

func (r *Repo) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID       int64           `json:"id"`
		Name     string          `json:"name"`
		FullName string          `json:"full_name"`
		Private  bool            `json:"private"`
		Owner    json.RawMessage `json:"owner"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.ID = aux.ID
	r.Name = aux.Name
	r.FullName = aux.FullName
	r.Private = aux.Private
	var ownerString string
	if err := json.Unmarshal(aux.Owner, &ownerString); err == nil {
		r.Owner = ownerString
	} else {
		var ownerObject struct {
			Login string `json:"login"`
		}
		_ = json.Unmarshal(aux.Owner, &ownerObject)
		r.Owner = ownerObject.Login
	}
	if r.FullName == "" && r.Owner != "" && r.Name != "" {
		r.FullName = r.Owner + "/" + r.Name
	}
	r.Raw = append(r.Raw[:0], data...)
	return nil
}

type RunRecord struct {
	ID           int64           `json:"id"`
	Repo         string          `json:"repo"`
	WorkflowID   int64           `json:"workflow_id"`
	WorkflowName string          `json:"workflow_name"`
	WorkflowPath string          `json:"workflow_path"`
	RunNumber    int64           `json:"run_number"`
	RunAttempt   int64           `json:"run_attempt"`
	Event        string          `json:"event"`
	Branch       string          `json:"branch"`
	Actor        string          `json:"actor"`
	Status       string          `json:"status"`
	Conclusion   string          `json:"conclusion"`
	CreatedAt    string          `json:"created_at"`
	RunStartedAt string          `json:"run_started_at"`
	HTMLURL      string          `json:"html_url"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

type JobRecord struct {
	ID           int64           `json:"id"`
	RunID        int64           `json:"run_id"`
	Repo         string          `json:"repo"`
	WorkflowName string          `json:"workflow_name"`
	WorkflowPath string          `json:"workflow_path"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	Conclusion   string          `json:"conclusion"`
	StartedAt    string          `json:"started_at"`
	CompletedAt  string          `json:"completed_at"`
	DurationSecs float64         `json:"duration_seconds"`
	RunnerName   string          `json:"runner_name"`
	RunnerGroup  string          `json:"runner_group"`
	Runner       RunnerMetadata  `json:"runner"`
	Labels       []string        `json:"labels"`
	HTMLURL      string          `json:"html_url"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

type SummaryGroup struct {
	Key          string            `json:"key"`
	Values       map[string]string `json:"values"`
	Jobs         int               `json:"jobs"`
	Runs         int               `json:"runs"`
	Counts       map[string]int    `json:"counts"`
	TotalSeconds float64           `json:"total_seconds"`
	TotalMinutes float64           `json:"total_minutes"`
	AverageSecs  float64           `json:"average_seconds"`
	LongestSecs  float64           `json:"longest_seconds"`
}

type Summary struct {
	GeneratedAt  string         `json:"generated_at"`
	CachePath    string         `json:"cache_path"`
	Filters      QueryFilters   `json:"filters"`
	TotalJobs    int            `json:"total_jobs"`
	TotalRuns    int            `json:"total_runs"`
	TotalSeconds float64        `json:"total_seconds"`
	TotalMinutes float64        `json:"total_minutes"`
	Counts       map[string]int `json:"counts"`
	GroupBy      []string       `json:"group_by,omitempty"`
	Groups       []SummaryGroup `json:"groups,omitempty"`
}

type QueryFilters struct {
	Repo  string `json:"repo,omitempty"`
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type IngestResult struct {
	Account       string `json:"account"`
	ReposSeen     int    `json:"repos_seen"`
	ReposIngested int    `json:"repos_ingested"`
	RunsUpserted  int    `json:"runs_upserted"`
	JobsUpserted  int    `json:"jobs_upserted"`
	CachePath     string `json:"cache_path"`
}

type ImportResult struct {
	ReposImported int    `json:"repos_imported"`
	RunsImported  int    `json:"runs_imported"`
	JobsImported  int    `json:"jobs_imported"`
	CachePath     string `json:"cache_path"`
}

type ExportPayload struct {
	ExportedAt string      `json:"exported_at"`
	Runs       []RunRecord `json:"runs"`
	Jobs       []JobRecord `json:"jobs"`
	Repos      []Repo      `json:"repos,omitempty"`
}

type WorkflowRunAPI struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	WorkflowID   int64  `json:"workflow_id"`
	RunNumber    int64  `json:"run_number"`
	RunAttempt   int64  `json:"run_attempt"`
	Event        string `json:"event"`
	HeadBranch   string `json:"head_branch"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	CreatedAt    string `json:"created_at"`
	RunStartedAt string `json:"run_started_at"`
	HTMLURL      string `json:"html_url"`
	Actor        struct {
		Login string `json:"login"`
	} `json:"actor"`
	raw json.RawMessage
}

type WorkflowJobAPI struct {
	ID              int64    `json:"id"`
	RunID           int64    `json:"run_id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	Conclusion      string   `json:"conclusion"`
	StartedAt       string   `json:"started_at"`
	CompletedAt     string   `json:"completed_at"`
	HTMLURL         string   `json:"html_url"`
	RunnerName      string   `json:"runner_name"`
	RunnerGroupName string   `json:"runner_group_name"`
	Labels          []string `json:"labels"`
	raw             json.RawMessage
}

func main() {
	stdout := os.Stdout
	stderr := os.Stderr
	cachePath := defaultCachePath()
	cache, err := OpenCache(cachePath)
	if err != nil {
		log.Fatalf("open cache: %v", err)
	}
	defer cache.Close()

	client, err := api.DefaultRESTClient()
	if err != nil {
		client = nil
	}

	app := &App{stdout: stdout, stderr: stderr, client: client, cache: cache, now: time.Now}
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printHelp(a.stdout)
		return nil
	}

	switch args[0] {
	case "doctor":
		return a.cmdDoctor(ctx, args[1:])
	case "accounts":
		return a.cmdAccounts(ctx, args[1:])
	case "repos":
		return a.cmdRepos(ctx, args[1:])
	case "ingest":
		return a.cmdIngest(ctx, args[1:])
	case "runs":
		return a.cmdRuns(args[1:])
	case "jobs":
		return a.cmdJobs(args[1:])
	case "summary":
		return a.cmdSummary(args[1:])
	case "export":
		return a.cmdExport(args[1:])
	case "import":
		return a.cmdImport(args[1:])
	case "serve":
		return a.cmdServe(args[1:])
	case "api":
		return a.cmdAPI(args[1:])
	case "cache":
		return a.cmdCache(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `gh actions-usage: cached GitHub Actions usage analytics

Usage:
  gh actions-usage doctor [--json]
  gh actions-usage accounts list [--json]
  gh actions-usage repos list --account @me|ORG [--json]
  gh actions-usage ingest actions --account @me|ORG [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
  gh actions-usage summary [--group-by repo,workflow-path,job,runner-image] [--json]
  gh actions-usage runs list [--json]
  gh actions-usage jobs list [--limit 50] [--json]
  gh actions-usage import --in report.json [--json]
  gh actions-usage serve [--listen 127.0.0.1:8080] [--open]
  gh actions-usage export --out report.json
  gh actions-usage api get /user
  gh actions-usage cache path|stats|clear

Data is cached locally in SQLite and repeated ingest runs upsert raw run/job data.`)
}

func (a *App) cmdDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result := map[string]any{
		"extension":  appName,
		"cache_path": a.cache.Path(),
		"auth":       map[string]any{"ok": false, "source": authSource()},
		"api":        map[string]any{"ok": false},
		"cache":      map[string]any{"ok": true},
	}

	if a.client != nil {
		var user struct {
			Login string `json:"login"`
		}
		err := a.client.Get("user", &user)
		if err == nil {
			result["auth"] = map[string]any{"ok": true, "source": authSource(), "login": user.Login}
			result["api"] = map[string]any{"ok": true}
		} else {
			result["api"] = map[string]any{"ok": false, "error": err.Error()}
		}
	}

	stats, err := a.cache.Stats()
	if err != nil {
		result["cache"] = map[string]any{"ok": false, "error": err.Error()}
	} else {
		result["cache"] = map[string]any{"ok": true, "stats": stats}
	}

	if *jsonOut {
		return writeJSON(a.stdout, result)
	}

	fmt.Fprintf(a.stdout, "extension: %s\n", appName)
	fmt.Fprintf(a.stdout, "cache: %s\n", a.cache.Path())
	if auth, ok := result["auth"].(map[string]any); ok && auth["ok"] == true {
		fmt.Fprintf(a.stdout, "auth: ok (%s)\n", auth["source"])
	} else {
		fmt.Fprintln(a.stdout, "auth: missing or invalid")
	}
	return nil
}

func authSource() string {
	if os.Getenv("GH_TOKEN") != "" {
		return "env:GH_TOKEN"
	}
	if os.Getenv("GITHUB_TOKEN") != "" {
		return "env:GITHUB_TOKEN"
	}
	return "gh"
}

func (a *App) cmdAccounts(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: accounts list [--json]")
	}
	fs := flag.NewFlagSet("accounts list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if a.client == nil {
		return fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := a.client.Get("user", &user); err != nil {
		return err
	}
	accounts := []Account{{Kind: "user", Login: user.Login}}

	var orgs []struct {
		Login string `json:"login"`
	}
	if err := a.client.Get("user/orgs?per_page=100", &orgs); err == nil {
		for _, org := range orgs {
			accounts = append(accounts, Account{Kind: "org", Login: org.Login})
		}
	}

	if *jsonOut {
		return writeJSON(a.stdout, accounts)
	}
	for _, account := range accounts {
		fmt.Fprintf(a.stdout, "%s\t%s\n", account.Kind, account.Login)
	}
	return nil
}

func (a *App) cmdRepos(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: repos list --account @me|ORG [--json]")
	}
	fs := flag.NewFlagSet("repos list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	account := fs.String("account", "@me", "account to inspect")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	repos, err := a.fetchRepos(ctx, *account)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(a.stdout, repos)
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "repo\tprivate")
	for _, repo := range repos {
		fmt.Fprintf(w, "%s\t%t\n", repo.FullName, repo.Private)
	}
	return w.Flush()
}

func (a *App) cmdIngest(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "actions" {
		return fmt.Errorf("usage: ingest actions --account @me|ORG [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD]")
	}
	fs := flag.NewFlagSet("ingest actions", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	account := fs.String("account", "@me", "account to inspect")
	repoFilter := fs.String("repo", "", "comma-separated repositories to ingest")
	since := fs.String("since", "", "start date YYYY-MM-DD")
	until := fs.String("until", "", "end date YYYY-MM-DD")
	days := fs.Int("days", 30, "days back when --since is absent")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if a.client == nil {
		return fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}
	created, err := createdQuery(*since, *until, *days, a.now())
	if err != nil {
		return err
	}

	repos, err := a.fetchRepos(ctx, *account)
	if err != nil {
		return err
	}
	selected := filterRepos(repos, *repoFilter)
	if len(selected) == 0 {
		return fmt.Errorf("no repositories matched")
	}

	result := IngestResult{Account: *account, ReposSeen: len(repos), ReposIngested: len(selected), CachePath: a.cache.Path()}
	for _, repo := range selected {
		if err := a.cache.UpsertRepo(repo); err != nil {
			return err
		}
		runs, err := a.fetchRuns(ctx, repo, created)
		if err != nil {
			return err
		}
		workflowPaths := map[int64]string{}
		for _, run := range runs {
			if _, ok := workflowPaths[run.WorkflowID]; !ok && run.WorkflowID != 0 {
				workflowPaths[run.WorkflowID] = a.fetchWorkflowPath(repo, run.WorkflowID)
			}
			run.WorkflowPath = workflowPaths[run.WorkflowID]
			if err := a.cache.UpsertRun(run); err != nil {
				return err
			}
			result.RunsUpserted++

			jobs, err := a.fetchJobs(ctx, repo, run)
			if err != nil {
				return err
			}
			for _, job := range jobs {
				if err := a.cache.UpsertJob(job); err != nil {
					return err
				}
				result.JobsUpserted++
			}
		}
	}

	if *jsonOut {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "ingested %d repos, %d runs, %d jobs\n", result.ReposIngested, result.RunsUpserted, result.JobsUpserted)
	fmt.Fprintf(a.stdout, "cache: %s\n", result.CachePath)
	return nil
}

func (a *App) cmdRuns(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: runs list [--repo OWNER/NAME] [--limit 50] [--json]")
	}
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	repo := fs.String("repo", "", "repository filter")
	limit := fs.Int("limit", 50, "maximum rows")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	runs, err := a.cache.ListRuns(QueryFilters{Repo: *repo, Limit: *limit})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(a.stdout, runs)
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "started\trepo\tworkflow\tresult\turl")
	for _, run := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", dateOnly(run.RunStartedAt), run.Repo, firstNonEmpty(run.WorkflowPath, run.WorkflowName), run.Conclusion, run.HTMLURL)
	}
	return w.Flush()
}

func (a *App) cmdJobs(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: jobs list [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 50] [--json]")
	}
	fs := flag.NewFlagSet("jobs list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	repo := fs.String("repo", "", "repository filter")
	since := fs.String("since", "", "start date")
	until := fs.String("until", "", "end date")
	limit := fs.Int("limit", 50, "maximum rows")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	jobs, err := a.cache.ListJobs(QueryFilters{Repo: *repo, Since: *since, Until: *until, Limit: *limit})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(a.stdout, jobs)
	}
	printJobs(a.stdout, jobs)
	return nil
}

func (a *App) cmdSummary(args []string) error {
	fs := flag.NewFlagSet("summary", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	groupBy := fs.String("group-by", "repo,workflow-path,job,runner-image", "comma-separated dimensions")
	repo := fs.String("repo", "", "repository filter")
	since := fs.String("since", "", "start date")
	until := fs.String("until", "", "end date")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	jobs, err := a.cache.ListJobs(QueryFilters{Repo: *repo, Since: *since, Until: *until})
	if err != nil {
		return err
	}
	summary := buildSummary(a.cache.Path(), jobs, QueryFilters{Repo: *repo, Since: *since, Until: *until}, splitCSV(*groupBy), a.now())
	if *jsonOut {
		return writeJSON(a.stdout, summary)
	}
	printSummary(a.stdout, summary)
	return nil
}

func (a *App) cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	out := fs.String("out", "", "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	jobs, err := a.cache.ListJobs(QueryFilters{})
	if err != nil {
		return err
	}
	runs, err := a.cache.ListRuns(QueryFilters{})
	if err != nil {
		return err
	}
	repos, err := a.cache.ListRepos()
	if err != nil {
		return err
	}
	payload := ExportPayload{Runs: runs, Jobs: jobs, Repos: repos, ExportedAt: a.now().Format(time.RFC3339)}
	file, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "exported %d repos, %d runs, and %d jobs to %s\n", len(repos), len(runs), len(jobs), *out)
	return nil
}

func (a *App) cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	in := fs.String("in", "", "input export JSON file")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("--in is required")
	}
	file, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer file.Close()

	var payload ExportPayload
	if err := json.NewDecoder(file).Decode(&payload); err != nil {
		return err
	}
	result := ImportResult{CachePath: a.cache.Path()}
	for _, repo := range payload.Repos {
		if err := a.cache.UpsertRepo(repo); err != nil {
			return err
		}
		result.ReposImported++
	}
	for _, run := range payload.Runs {
		if err := a.cache.UpsertRun(run); err != nil {
			return err
		}
		result.RunsImported++
	}
	for _, job := range payload.Jobs {
		if err := a.cache.UpsertJob(job); err != nil {
			return err
		}
		result.JobsImported++
	}
	if *jsonOut {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "imported %d repos, %d runs, %d jobs\n", result.ReposImported, result.RunsImported, result.JobsImported)
	fmt.Fprintf(a.stdout, "cache: %s\n", result.CachePath)
	return nil
}

func (a *App) cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	listen := fs.String("listen", "127.0.0.1:8080", "listen address")
	openBrowser := fs.Bool("open", false, "open browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	handler, err := a.webHandler()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	addr := "http://" + ln.Addr().String()
	fmt.Fprintf(a.stdout, "serving %s\n", addr)
	if *openBrowser {
		_ = browser.New("", a.stdout, a.stderr).Browse(addr)
	}
	return http.Serve(ln, handler)
}

func (a *App) cmdAPI(args []string) error {
	if len(args) < 2 || args[0] != "get" {
		return fmt.Errorf("usage: api get /path")
	}
	if a.client == nil {
		return fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}
	path := strings.TrimPrefix(args[1], "/")
	var payload any
	if err := a.client.Get(path, &payload); err != nil {
		return err
	}
	return writeJSON(a.stdout, payload)
}

func (a *App) cmdCache(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cache path|stats|clear")
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(a.stdout, a.cache.Path())
		return nil
	case "stats":
		stats, err := a.cache.Stats()
		if err != nil {
			return err
		}
		return writeJSON(a.stdout, stats)
	case "clear":
		return a.cache.Clear()
	default:
		return fmt.Errorf("usage: cache path|stats|clear")
	}
}

func (a *App) fetchRepos(ctx context.Context, account string) ([]Repo, error) {
	if a.client == nil {
		return nil, fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}
	if account == "@me" {
		var user struct {
			Login string `json:"login"`
		}
		if err := a.client.Get("user", &user); err != nil {
			return nil, err
		}
		account = user.Login
		return fetchPaged[Repo](a.client, "user/repos?visibility=all&affiliation=owner,collaborator,organization_member&sort=full_name")
	}
	return fetchPaged[Repo](a.client, "orgs/"+url.PathEscape(account)+"/repos?type=all&sort=full_name")
}

func (a *App) fetchRuns(ctx context.Context, repo Repo, created string) ([]RunRecord, error) {
	endpoint := fmt.Sprintf("repos/%s/actions/runs?per_page=100&created=%s", repoPath(repo.FullName), url.QueryEscape(created))
	rawRuns, err := fetchPagedEnvelope[WorkflowRunAPI](a.client, endpoint, "workflow_runs")
	if err != nil {
		return nil, err
	}
	runs := make([]RunRecord, 0, len(rawRuns))
	for _, raw := range rawRuns {
		runs = append(runs, RunRecord{
			ID:           raw.ID,
			Repo:         repo.FullName,
			WorkflowID:   raw.WorkflowID,
			WorkflowName: raw.Name,
			RunNumber:    raw.RunNumber,
			RunAttempt:   raw.RunAttempt,
			Event:        raw.Event,
			Branch:       raw.HeadBranch,
			Actor:        raw.Actor.Login,
			Status:       raw.Status,
			Conclusion:   raw.Conclusion,
			CreatedAt:    raw.CreatedAt,
			RunStartedAt: raw.RunStartedAt,
			HTMLURL:      raw.HTMLURL,
			Raw:          raw.raw,
		})
	}
	return runs, nil
}

func (a *App) fetchWorkflowPath(repo Repo, workflowID int64) string {
	var response struct {
		Path string `json:"path"`
	}
	endpoint := fmt.Sprintf("repos/%s/actions/workflows/%d", repoPath(repo.FullName), workflowID)
	if err := a.client.Get(endpoint, &response); err != nil {
		return ""
	}
	return response.Path
}

func (a *App) fetchJobs(ctx context.Context, repo Repo, run RunRecord) ([]JobRecord, error) {
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/jobs?filter=all&per_page=100", repoPath(repo.FullName), run.ID)
	rawJobs, err := fetchPagedEnvelope[WorkflowJobAPI](a.client, endpoint, "jobs")
	if err != nil {
		return nil, err
	}
	jobs := make([]JobRecord, 0, len(rawJobs))
	for _, raw := range rawJobs {
		metadata := runnerMetadata(raw.Labels)
		jobs = append(jobs, JobRecord{
			ID:           raw.ID,
			RunID:        run.ID,
			Repo:         repo.FullName,
			WorkflowName: run.WorkflowName,
			WorkflowPath: run.WorkflowPath,
			Name:         raw.Name,
			Status:       raw.Status,
			Conclusion:   firstNonEmpty(raw.Conclusion, raw.Status, "unknown"),
			StartedAt:    raw.StartedAt,
			CompletedAt:  raw.CompletedAt,
			DurationSecs: durationSeconds(raw.StartedAt, raw.CompletedAt),
			RunnerName:   raw.RunnerName,
			RunnerGroup:  raw.RunnerGroupName,
			Runner:       metadata,
			Labels:       raw.Labels,
			HTMLURL:      raw.HTMLURL,
			Raw:          raw.raw,
		})
	}
	return jobs, nil
}

type rawCapture[T any] struct {
	target *T
}

func (r *rawCapture[T]) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, r.target); err != nil {
		return err
	}
	switch v := any(r.target).(type) {
	case *WorkflowRunAPI:
		v.raw = append(v.raw[:0], data...)
	case *WorkflowJobAPI:
		v.raw = append(v.raw[:0], data...)
	}
	return nil
}

func fetchPaged[T any](client APIClient, base string) ([]T, error) {
	var out []T
	for page := 1; ; page++ {
		path := withPage(base, page)
		var pageItems []T
		if err := client.Get(path, &pageItems); err != nil {
			return nil, err
		}
		out = append(out, pageItems...)
		if len(pageItems) < 100 {
			break
		}
	}
	return out, nil
}

func fetchPagedEnvelope[T any](client APIClient, base string, field string) ([]T, error) {
	var out []T
	for page := 1; ; page++ {
		path := withPage(base, page)
		var raw map[string]json.RawMessage
		if err := client.Get(path, &raw); err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw[field], &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			var value T
			capture := rawCapture[T]{target: &value}
			if err := capture.UnmarshalJSON(item); err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		if len(items) < 100 {
			break
		}
	}
	return out, nil
}

func withPage(base string, page int) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%spage=%d&per_page=100", base, sep, page)
}

func repoPath(fullName string) string {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return url.PathEscape(fullName)
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

func defaultCachePath() string {
	if override := os.Getenv("GH_ACTIONS_USAGE_CACHE"); override != "" {
		return override
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appName, "cache.db")
}

func createdQuery(since string, until string, days int, now time.Time) (string, error) {
	if since != "" {
		if _, err := time.Parse(dateFormat, since); err != nil {
			return "", fmt.Errorf("invalid --since date %q", since)
		}
	}
	if until != "" {
		if _, err := time.Parse(dateFormat, until); err != nil {
			return "", fmt.Errorf("invalid --until date %q", until)
		}
	}
	switch {
	case since != "" && until != "":
		return since + ".." + until, nil
	case since != "":
		return ">=" + since, nil
	case until != "":
		return "<=" + until, nil
	default:
		return ">=" + now.AddDate(0, 0, -days).Format(dateFormat), nil
	}
}

func filterRepos(repos []Repo, filter string) []Repo {
	if strings.TrimSpace(filter) == "" {
		return repos
	}
	allowed := map[string]bool{}
	for _, repo := range splitCSV(filter) {
		allowed[repo] = true
	}
	var out []Repo
	for _, repo := range repos {
		if allowed[repo.FullName] {
			out = append(out, repo)
		}
	}
	return out
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func runnerMetadata(labels []string) RunnerMetadata {
	meta := RunnerMetadata{Type: "github-hosted", OS: "unknown", Arch: "unknown", Image: "unknown"}
	for _, label := range labels {
		norm := strings.ToLower(strings.TrimSpace(label))
		switch {
		case norm == "self-hosted":
			meta.Type = "self-hosted"
		case norm == "linux" || strings.HasPrefix(norm, "ubuntu"):
			meta.OS = "Linux"
		case norm == "macos" || strings.HasPrefix(norm, "macos"):
			meta.OS = "macOS"
		case norm == "windows" || strings.HasPrefix(norm, "windows"):
			meta.OS = "Windows"
		}
		switch norm {
		case "x64", "amd64", "x86_64":
			meta.Arch = "X64"
		case "arm64", "aarch64":
			meta.Arch = "ARM64"
		case "arm":
			meta.Arch = "ARM"
		}
		if strings.HasPrefix(norm, "ubuntu-") || strings.HasPrefix(norm, "macos-") || strings.HasPrefix(norm, "windows-") {
			meta.Image = label
		}
	}
	return meta
}

func durationSeconds(start string, end string) float64 {
	if start == "" || end == "" {
		return 0
	}
	started, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return 0
	}
	completed, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return 0
	}
	d := completed.Sub(started).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

func buildSummary(cachePath string, jobs []JobRecord, filters QueryFilters, groupBy []string, now time.Time) Summary {
	summary := Summary{
		GeneratedAt:  now.Format(time.RFC3339),
		CachePath:    cachePath,
		Filters:      filters,
		Counts:       map[string]int{},
		GroupBy:      groupBy,
		TotalJobs:    len(jobs),
		TotalRuns:    countRuns(jobs),
		TotalSeconds: sumDuration(jobs),
	}
	summary.TotalMinutes = summary.TotalSeconds / 60
	summary.Groups = summarize(jobs, groupBy)
	for _, job := range jobs {
		summary.Counts[firstNonEmpty(job.Conclusion, "unknown")]++
	}
	return summary
}

func summarize(jobs []JobRecord, groupBy []string) []SummaryGroup {
	if len(groupBy) == 0 {
		return nil
	}
	groups := map[string]*SummaryGroup{}
	for _, job := range jobs {
		values := map[string]string{}
		keyParts := make([]string, 0, len(groupBy))
		for _, dim := range groupBy {
			value := dimension(job, dim)
			values[dim] = value
			keyParts = append(keyParts, value)
		}
		key := strings.Join(keyParts, "\t")
		group := groups[key]
		if group == nil {
			group = &SummaryGroup{Key: key, Values: values, Counts: map[string]int{}}
			groups[key] = group
		}
		group.Jobs++
		group.Counts[firstNonEmpty(job.Conclusion, "unknown")]++
		group.TotalSeconds += job.DurationSecs
		if job.DurationSecs > group.LongestSecs {
			group.LongestSecs = job.DurationSecs
		}
	}
	out := make([]SummaryGroup, 0, len(groups))
	for _, group := range groups {
		group.TotalMinutes = group.TotalSeconds / 60
		if group.Jobs > 0 {
			group.AverageSecs = group.TotalSeconds / float64(group.Jobs)
		}
		group.Runs = 0
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalSeconds == out[j].TotalSeconds {
			return out[i].Key < out[j].Key
		}
		return out[i].TotalSeconds > out[j].TotalSeconds
	})
	return out
}

func dimension(job JobRecord, dim string) string {
	switch dim {
	case "date":
		return dateOnly(job.StartedAt)
	case "repo":
		return firstNonEmpty(job.Repo, "unknown")
	case "workflow", "workflow-name":
		return firstNonEmpty(job.WorkflowName, "unknown")
	case "workflow-path":
		return firstNonEmpty(job.WorkflowPath, job.WorkflowName, "unknown")
	case "job":
		return firstNonEmpty(job.Name, "unknown")
	case "runner", "runner-name":
		return firstNonEmpty(job.RunnerName, "unknown")
	case "runner-group":
		return firstNonEmpty(job.RunnerGroup, "unknown")
	case "runner-type":
		return firstNonEmpty(job.Runner.Type, "unknown")
	case "runner-os", "os":
		return firstNonEmpty(job.Runner.OS, "unknown")
	case "runner-arch", "arch":
		return firstNonEmpty(job.Runner.Arch, "unknown")
	case "runner-image", "image":
		return firstNonEmpty(job.Runner.Image, "unknown")
	case "platform":
		return firstNonEmpty(job.Runner.OS, "unknown") + "/" + firstNonEmpty(job.Runner.Arch, "unknown")
	case "conclusion":
		return firstNonEmpty(job.Conclusion, "unknown")
	default:
		return "unknown"
	}
}

func countRuns(jobs []JobRecord) int {
	seen := map[int64]bool{}
	for _, job := range jobs {
		seen[job.RunID] = true
	}
	return len(seen)
}

func sumDuration(jobs []JobRecord) float64 {
	var total float64
	for _, job := range jobs {
		total += job.DurationSecs
	}
	return total
}

func printSummary(w io.Writer, summary Summary) {
	fmt.Fprintf(w, "jobs: %d\n", summary.TotalJobs)
	fmt.Fprintf(w, "runs: %d\n", summary.TotalRuns)
	fmt.Fprintf(w, "runtime: %.1f minutes\n", summary.TotalMinutes)
	if len(summary.Groups) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := append([]string{}, summary.GroupBy...)
	header = append(header, "jobs", "minutes", "avg", "longest")
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, group := range summary.Groups {
		row := make([]string, 0, len(summary.GroupBy)+4)
		for _, dim := range summary.GroupBy {
			row = append(row, group.Values[dim])
		}
		row = append(row, strconv.Itoa(group.Jobs), fmt.Sprintf("%.1f", group.TotalMinutes), formatDuration(group.AverageSecs), formatDuration(group.LongestSecs))
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

func printJobs(w io.Writer, jobs []JobRecord) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "started\trepo\tworkflow\tjob\trunner\tresult\tduration")
	for _, job := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			dateOnly(job.StartedAt), job.Repo, firstNonEmpty(job.WorkflowPath, job.WorkflowName), job.Name,
			firstNonEmpty(job.Runner.Image, job.Runner.OS), job.Conclusion, formatDuration(job.DurationSecs))
	}
	tw.Flush()
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func dateOnly(value string) string {
	if len(value) >= len(dateFormat) {
		return value[:len(dateFormat)]
	}
	return "unknown"
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "0s"
	}
	d := time.Duration(seconds * float64(time.Second)).Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

type Cache struct {
	path string
	db   *sql.DB
}

func OpenCache(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	cache := &Cache{path: path, db: db}
	if err := cache.init(); err != nil {
		db.Close()
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Path() string { return c.path }
func (c *Cache) Close() error { return c.db.Close() }

func (c *Cache) init() error {
	statements := []string{
		`create table if not exists repos (
			full_name text primary key,
			id integer,
			owner text,
			name text,
			private integer,
			raw_json text,
			updated_at text default current_timestamp
		)`,
		`create table if not exists runs (
			id integer primary key,
			repo text,
			workflow_id integer,
			workflow_name text,
			workflow_path text,
			run_number integer,
			run_attempt integer,
			event text,
			branch text,
			actor text,
			status text,
			conclusion text,
			created_at text,
			run_started_at text,
			html_url text,
			raw_json text,
			updated_at text default current_timestamp
		)`,
		`create table if not exists jobs (
			id integer primary key,
			run_id integer,
			repo text,
			workflow_name text,
			workflow_path text,
			name text,
			status text,
			conclusion text,
			started_at text,
			completed_at text,
			duration_seconds real,
			runner_name text,
			runner_group text,
			runner_type text,
			runner_os text,
			runner_arch text,
			runner_image text,
			labels_json text,
			html_url text,
			raw_json text,
			updated_at text default current_timestamp
		)`,
		`create index if not exists idx_jobs_repo_started on jobs(repo, started_at)`,
		`create index if not exists idx_runs_repo_started on runs(repo, run_started_at)`,
	}
	for _, stmt := range statements {
		if _, err := c.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) UpsertRepo(repo Repo) error {
	raw, _ := json.Marshal(repo)
	_, err := c.db.Exec(`insert into repos(full_name,id,owner,name,private,raw_json,updated_at)
		values(?,?,?,?,?,?,current_timestamp)
		on conflict(full_name) do update set id=excluded.id, owner=excluded.owner, name=excluded.name, private=excluded.private, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		repo.FullName, repo.ID, repo.Owner, repo.Name, boolInt(repo.Private), string(firstRaw(repo.Raw, raw)))
	return err
}

func (c *Cache) UpsertRun(run RunRecord) error {
	raw, _ := json.Marshal(run)
	_, err := c.db.Exec(`insert into runs(id,repo,workflow_id,workflow_name,workflow_path,run_number,run_attempt,event,branch,actor,status,conclusion,created_at,run_started_at,html_url,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(id) do update set repo=excluded.repo, workflow_id=excluded.workflow_id, workflow_name=excluded.workflow_name, workflow_path=excluded.workflow_path, run_number=excluded.run_number, run_attempt=excluded.run_attempt, event=excluded.event, branch=excluded.branch, actor=excluded.actor, status=excluded.status, conclusion=excluded.conclusion, created_at=excluded.created_at, run_started_at=excluded.run_started_at, html_url=excluded.html_url, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		run.ID, run.Repo, run.WorkflowID, run.WorkflowName, run.WorkflowPath, run.RunNumber, run.RunAttempt, run.Event, run.Branch, run.Actor, run.Status, run.Conclusion, run.CreatedAt, run.RunStartedAt, run.HTMLURL, string(firstRaw(run.Raw, raw)))
	return err
}

func (c *Cache) UpsertJob(job JobRecord) error {
	labels, _ := json.Marshal(job.Labels)
	raw, _ := json.Marshal(job)
	_, err := c.db.Exec(`insert into jobs(id,run_id,repo,workflow_name,workflow_path,name,status,conclusion,started_at,completed_at,duration_seconds,runner_name,runner_group,runner_type,runner_os,runner_arch,runner_image,labels_json,html_url,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(id) do update set run_id=excluded.run_id, repo=excluded.repo, workflow_name=excluded.workflow_name, workflow_path=excluded.workflow_path, name=excluded.name, status=excluded.status, conclusion=excluded.conclusion, started_at=excluded.started_at, completed_at=excluded.completed_at, duration_seconds=excluded.duration_seconds, runner_name=excluded.runner_name, runner_group=excluded.runner_group, runner_type=excluded.runner_type, runner_os=excluded.runner_os, runner_arch=excluded.runner_arch, runner_image=excluded.runner_image, labels_json=excluded.labels_json, html_url=excluded.html_url, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		job.ID, job.RunID, job.Repo, job.WorkflowName, job.WorkflowPath, job.Name, job.Status, job.Conclusion, job.StartedAt, job.CompletedAt, job.DurationSecs, job.RunnerName, job.RunnerGroup, job.Runner.Type, job.Runner.OS, job.Runner.Arch, job.Runner.Image, string(labels), job.HTMLURL, string(firstRaw(job.Raw, raw)))
	return err
}

func (c *Cache) ListRuns(filters QueryFilters) ([]RunRecord, error) {
	query := `select id,repo,workflow_id,workflow_name,workflow_path,run_number,run_attempt,event,branch,actor,status,conclusion,created_at,run_started_at,html_url,raw_json from runs`
	where, args := filtersWhere(filters, "run_started_at")
	if where != "" {
		query += " where " + where
	}
	query += " order by run_started_at desc"
	if filters.Limit > 0 {
		query += fmt.Sprintf(" limit %d", filters.Limit)
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []RunRecord
	for rows.Next() {
		var run RunRecord
		var raw string
		if err := rows.Scan(&run.ID, &run.Repo, &run.WorkflowID, &run.WorkflowName, &run.WorkflowPath, &run.RunNumber, &run.RunAttempt, &run.Event, &run.Branch, &run.Actor, &run.Status, &run.Conclusion, &run.CreatedAt, &run.RunStartedAt, &run.HTMLURL, &raw); err != nil {
			return nil, err
		}
		run.Raw = json.RawMessage(raw)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (c *Cache) ListRepos() ([]Repo, error) {
	rows, err := c.db.Query(`select id,owner,name,full_name,private,raw_json from repos order by full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var repos []Repo
	for rows.Next() {
		var repo Repo
		var private int
		var raw string
		if err := rows.Scan(&repo.ID, &repo.Owner, &repo.Name, &repo.FullName, &private, &raw); err != nil {
			return nil, err
		}
		repo.Private = private == 1
		repo.Raw = json.RawMessage(raw)
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (c *Cache) ListJobs(filters QueryFilters) ([]JobRecord, error) {
	query := `select id,run_id,repo,workflow_name,workflow_path,name,status,conclusion,started_at,completed_at,duration_seconds,runner_name,runner_group,runner_type,runner_os,runner_arch,runner_image,labels_json,html_url,raw_json from jobs`
	where, args := filtersWhere(filters, "started_at")
	if where != "" {
		query += " where " + where
	}
	query += " order by duration_seconds desc, started_at desc"
	if filters.Limit > 0 {
		query += fmt.Sprintf(" limit %d", filters.Limit)
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []JobRecord
	for rows.Next() {
		var job JobRecord
		var labels string
		var raw string
		if err := rows.Scan(&job.ID, &job.RunID, &job.Repo, &job.WorkflowName, &job.WorkflowPath, &job.Name, &job.Status, &job.Conclusion, &job.StartedAt, &job.CompletedAt, &job.DurationSecs, &job.RunnerName, &job.RunnerGroup, &job.Runner.Type, &job.Runner.OS, &job.Runner.Arch, &job.Runner.Image, &labels, &job.HTMLURL, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &job.Labels)
		job.Raw = json.RawMessage(raw)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func filtersWhere(filters QueryFilters, timeField string) (string, []any) {
	var parts []string
	var args []any
	if filters.Repo != "" {
		parts = append(parts, "repo = ?")
		args = append(args, filters.Repo)
	}
	if filters.Since != "" {
		parts = append(parts, timeField+" >= ?")
		args = append(args, filters.Since)
	}
	if filters.Until != "" {
		parts = append(parts, timeField+" <= ?")
		args = append(args, filters.Until+"T23:59:59Z")
	}
	return strings.Join(parts, " and "), args
}

func (c *Cache) Stats() (map[string]int, error) {
	tables := []string{"repos", "runs", "jobs"}
	stats := map[string]int{}
	for _, table := range tables {
		row := c.db.QueryRow("select count(*) from " + table)
		var count int
		if err := row.Scan(&count); err != nil {
			return nil, err
		}
		stats[table] = count
	}
	return stats, nil
}

func (c *Cache) Clear() error {
	for _, table := range []string{"jobs", "runs", "repos"} {
		if _, err := c.db.Exec("delete from " + table); err != nil {
			return err
		}
	}
	return nil
}

func firstRaw(raw json.RawMessage, fallback []byte) []byte {
	if len(raw) > 0 {
		return raw
	}
	return fallback
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (a *App) webHandler() (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, webIndexHTML)
	})
	mux.HandleFunc("/api/summary", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := a.cache.ListJobs(QueryFilters{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = writeJSON(w, buildSummary(a.cache.Path(), jobs, QueryFilters{}, []string{"repo", "workflow-path", "job", "runner-image"}, a.now()))
	})
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		limit := 500
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				limit = parsed
			}
		}
		jobs, err := a.cache.ListJobs(QueryFilters{Limit: limit})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = writeJSON(w, jobs)
	})
	return mux, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
