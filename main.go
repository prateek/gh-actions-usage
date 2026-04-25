package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

var (
	openCacheFunc  = OpenCache
	restClientFunc = func() (APIClient, error) {
		return api.DefaultRESTClient()
	}
	serveHTTPFunc = http.Serve
	browseURLFunc = func(addr string, stdout io.Writer, stderr io.Writer) error {
		return browser.New("", stdout, stderr).Browse(addr)
	}
)

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
	ID               int64           `json:"id"`
	Account          string          `json:"account,omitempty"`
	Owner            string          `json:"owner"`
	OwnerKind        string          `json:"owner_kind,omitempty"`
	Name             string          `json:"name"`
	FullName         string          `json:"full_name"`
	Private          bool            `json:"private"`
	BillingOwner     string          `json:"billing_owner,omitempty"`
	BillingOwnerKind string          `json:"billing_owner_kind,omitempty"`
	BillingPlan      string          `json:"billing_plan,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

func (r *Repo) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID               int64           `json:"id"`
		Account          string          `json:"account"`
		Name             string          `json:"name"`
		FullName         string          `json:"full_name"`
		Private          bool            `json:"private"`
		Owner            json.RawMessage `json:"owner"`
		OwnerKind        string          `json:"owner_kind"`
		BillingOwner     string          `json:"billing_owner"`
		BillingOwnerKind string          `json:"billing_owner_kind"`
		BillingPlan      string          `json:"billing_plan"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.ID = aux.ID
	r.Account = aux.Account
	r.Name = aux.Name
	r.FullName = aux.FullName
	r.Private = aux.Private
	r.OwnerKind = normalizeAccountKind(aux.OwnerKind)
	r.BillingOwner = aux.BillingOwner
	r.BillingOwnerKind = normalizeAccountKind(aux.BillingOwnerKind)
	r.BillingPlan = aux.BillingPlan
	var ownerString string
	if err := json.Unmarshal(aux.Owner, &ownerString); err == nil {
		r.Owner = ownerString
	} else {
		var ownerObject struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		}
		_ = json.Unmarshal(aux.Owner, &ownerObject)
		r.Owner = ownerObject.Login
		if ownerObject.Type != "" {
			r.OwnerKind = normalizeAccountKind(ownerObject.Type)
		}
	}
	if r.FullName == "" && r.Owner != "" && r.Name != "" {
		r.FullName = r.Owner + "/" + r.Name
	}
	r.Raw = append(r.Raw[:0], data...)
	return nil
}

type RunRecord struct {
	ID               int64           `json:"id"`
	Account          string          `json:"account,omitempty"`
	Repo             string          `json:"repo"`
	RepoOwner        string          `json:"repo_owner,omitempty"`
	RepoOwnerKind    string          `json:"repo_owner_kind,omitempty"`
	BillingOwner     string          `json:"billing_owner,omitempty"`
	BillingOwnerKind string          `json:"billing_owner_kind,omitempty"`
	BillingPlan      string          `json:"billing_plan,omitempty"`
	WorkflowID       int64           `json:"workflow_id"`
	WorkflowName     string          `json:"workflow_name"`
	WorkflowPath     string          `json:"workflow_path"`
	RunNumber        int64           `json:"run_number"`
	RunAttempt       int64           `json:"run_attempt"`
	Event            string          `json:"event"`
	Branch           string          `json:"branch"`
	Actor            string          `json:"actor"`
	Status           string          `json:"status"`
	Conclusion       string          `json:"conclusion"`
	CreatedAt        string          `json:"created_at"`
	RunStartedAt     string          `json:"run_started_at"`
	HTMLURL          string          `json:"html_url"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

type JobRecord struct {
	ID               int64           `json:"id"`
	RunID            int64           `json:"run_id"`
	Account          string          `json:"account,omitempty"`
	Repo             string          `json:"repo"`
	RepoOwner        string          `json:"repo_owner,omitempty"`
	RepoOwnerKind    string          `json:"repo_owner_kind,omitempty"`
	BillingOwner     string          `json:"billing_owner,omitempty"`
	BillingOwnerKind string          `json:"billing_owner_kind,omitempty"`
	BillingPlan      string          `json:"billing_plan,omitempty"`
	CostClass        string          `json:"cost_class,omitempty"`
	WorkflowName     string          `json:"workflow_name"`
	WorkflowPath     string          `json:"workflow_path"`
	Name             string          `json:"name"`
	Status           string          `json:"status"`
	Conclusion       string          `json:"conclusion"`
	StartedAt        string          `json:"started_at"`
	CompletedAt      string          `json:"completed_at"`
	DurationSecs     float64         `json:"duration_seconds"`
	RunnerName       string          `json:"runner_name"`
	RunnerGroup      string          `json:"runner_group"`
	Runner           RunnerMetadata  `json:"runner"`
	Labels           []string        `json:"labels"`
	HTMLURL          string          `json:"html_url"`
	Raw              json.RawMessage `json:"raw,omitempty"`
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
	Account          string `json:"account"`
	AccountsIngested int    `json:"accounts_ingested,omitempty"`
	ReposSeen        int    `json:"repos_seen"`
	ReposIngested    int    `json:"repos_ingested"`
	RunsUpserted     int    `json:"runs_upserted"`
	JobsUpserted     int    `json:"jobs_upserted"`
	CachePath        string `json:"cache_path"`
}

type IngestOptions struct {
	Account       string
	RepoFilter    string
	Since         string
	Until         string
	Days          int
	DaysSet       bool
	AccountPlans  map[string]string
	BillingOwners map[string]string
	BillingKinds  map[string]string
}

type ReportResult struct {
	Refresh IngestResult `json:"refresh"`
	Summary Summary      `json:"summary"`
}

type BillingUsageRecord struct {
	Key              string          `json:"key"`
	Account          string          `json:"account"`
	AccountKind      string          `json:"account_kind"`
	Date             string          `json:"date,omitempty"`
	Year             int             `json:"year,omitempty"`
	Month            int             `json:"month,omitempty"`
	Day              int             `json:"day,omitempty"`
	Product          string          `json:"product,omitempty"`
	SKU              string          `json:"sku,omitempty"`
	UnitType         string          `json:"unit_type,omitempty"`
	Model            string          `json:"model,omitempty"`
	OrganizationName string          `json:"organization_name,omitempty"`
	RepositoryName   string          `json:"repository_name,omitempty"`
	CostCenterID     string          `json:"cost_center_id,omitempty"`
	CostClass        string          `json:"cost_class"`
	Quantity         float64         `json:"quantity,omitempty"`
	GrossQuantity    float64         `json:"gross_quantity,omitempty"`
	DiscountQuantity float64         `json:"discount_quantity,omitempty"`
	NetQuantity      float64         `json:"net_quantity,omitempty"`
	PricePerUnit     float64         `json:"price_per_unit,omitempty"`
	GrossAmount      float64         `json:"gross_amount,omitempty"`
	DiscountAmount   float64         `json:"discount_amount,omitempty"`
	NetAmount        float64         `json:"net_amount,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

type BillingQueryFilters struct {
	Account      string `json:"account,omitempty"`
	Repo         string `json:"repo,omitempty"`
	Since        string `json:"since,omitempty"`
	Until        string `json:"until,omitempty"`
	Year         int    `json:"year,omitempty"`
	Month        int    `json:"month,omitempty"`
	Day          int    `json:"day,omitempty"`
	Product      string `json:"product,omitempty"`
	SKU          string `json:"sku,omitempty"`
	Organization string `json:"organization,omitempty"`
	CostCenterID string `json:"cost_center_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type BillingRefreshResult struct {
	AccountsRefreshed int      `json:"accounts_refreshed"`
	Accounts          []string `json:"accounts"`
	ItemsUpserted     int      `json:"items_upserted"`
	CachePath         string   `json:"cache_path"`
}

type BillingSummaryGroup struct {
	Key              string            `json:"key"`
	Values           map[string]string `json:"values"`
	Items            int               `json:"items"`
	GrossQuantity    float64           `json:"gross_quantity"`
	DiscountQuantity float64           `json:"discount_quantity"`
	NetQuantity      float64           `json:"net_quantity"`
	GrossAmount      float64           `json:"gross_amount"`
	DiscountAmount   float64           `json:"discount_amount"`
	NetAmount        float64           `json:"net_amount"`
}

type BillingSummary struct {
	GeneratedAt      string                `json:"generated_at"`
	CachePath        string                `json:"cache_path"`
	Filters          BillingQueryFilters   `json:"filters"`
	TotalItems       int                   `json:"total_items"`
	GrossQuantity    float64               `json:"gross_quantity"`
	DiscountQuantity float64               `json:"discount_quantity"`
	NetQuantity      float64               `json:"net_quantity"`
	GrossAmount      float64               `json:"gross_amount"`
	DiscountAmount   float64               `json:"discount_amount"`
	NetAmount        float64               `json:"net_amount"`
	GroupBy          []string              `json:"group_by,omitempty"`
	Groups           []BillingSummaryGroup `json:"groups,omitempty"`
}

type ImportResult struct {
	ReposImported   int    `json:"repos_imported"`
	RunsImported    int    `json:"runs_imported"`
	JobsImported    int    `json:"jobs_imported"`
	BillingImported int    `json:"billing_imported,omitempty"`
	CachePath       string `json:"cache_path"`
}

type ExportPayload struct {
	ExportedAt   string               `json:"exported_at"`
	Runs         []RunRecord          `json:"runs"`
	Jobs         []JobRecord          `json:"jobs"`
	Repos        []Repo               `json:"repos,omitempty"`
	BillingUsage []BillingUsageRecord `json:"billing_usage,omitempty"`
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
	os.Exit(runMain(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func runMain(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	if printHelpForArgs(stdout, args) {
		return 0
	}
	cachePath := defaultCachePath()
	cache, err := openCacheFunc(cachePath)
	if err != nil {
		fmt.Fprintf(stderr, "error: open cache: %v\n", err)
		return 1
	}
	defer cache.Close()

	client, err := restClientFunc()
	if err != nil {
		client = nil
	}

	app := &App{stdout: stdout, stderr: stderr, client: client, cache: cache, now: time.Now}
	if err := app.Run(ctx, args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) Run(ctx context.Context, args []string) error {
	if printHelpForArgs(a.stdout, args) {
		return nil
	}

	switch args[0] {
	case "doctor":
		return a.cmdDoctor(ctx, args[1:])
	case "accounts":
		return a.cmdAccounts(ctx, args[1:])
	case "repos":
		return a.cmdRepos(ctx, args[1:])
	case "report":
		return a.cmdReport(ctx, args[1:])
	case "billing":
		return a.cmdBilling(ctx, args[1:])
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
		return a.cmdServe(ctx, args[1:])
	case "api":
		return a.cmdAPI(args[1:])
	case "cache":
		return a.cmdCache(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printHelpForArgs(w io.Writer, args []string) bool {
	if len(args) == 0 || isHelpArg(args[0]) {
		printHelp(w)
		return true
	}
	if !hasHelpArg(args) {
		return false
	}
	switch args[0] {
	case "doctor":
		if len(args) > 1 && args[1] == "ingest" {
			fmt.Fprintln(w, "usage: doctor ingest actions --account @me|ORG [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD]")
		} else {
			fmt.Fprintln(w, "usage: doctor [--json]")
		}
	case "accounts":
		fmt.Fprintln(w, "usage: accounts list [--json]")
	case "repos":
		fmt.Fprintln(w, "usage: repos list --account @me|ORG [--json]")
	case "report":
		fmt.Fprintln(w, "usage: report --account @me|ORG[,ORG...] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--json]")
	case "billing":
		fmt.Fprintln(w, "usage: billing refresh|summary")
	case "summary":
		fmt.Fprintln(w, "usage: summary [--group-by repo,workflow-path,job,runner-image] [--json]")
	case "runs":
		fmt.Fprintln(w, "usage: runs list [--repo OWNER/NAME] [--limit 50] [--json]")
	case "jobs":
		fmt.Fprintln(w, "usage: jobs list [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 50] [--json]")
	case "import":
		fmt.Fprintln(w, "usage: import --in report.json [--json]")
	case "export":
		fmt.Fprintln(w, "usage: export --out report.json")
	case "serve":
		fmt.Fprintln(w, "usage: serve [--refresh] [--account @me|ORG] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--listen 127.0.0.1:8080] [--open]")
	case "api":
		fmt.Fprintln(w, "usage: api get /path")
	case "cache":
		fmt.Fprintln(w, "usage: cache path|stats|clear")
	default:
		printHelp(w)
	}
	return true
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `gh actions-usage: cached GitHub Actions and billing usage analytics

Usage:
  gh actions-usage doctor [--json]
  gh actions-usage accounts list [--json]
  gh actions-usage repos list --account @me|ORG [--json]
  gh actions-usage report --account @me|ORG[,ORG...] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--json]
  gh actions-usage billing refresh --account @me|ORG|enterprise:SLUG[,...] [--year YYYY] [--month M] [--json]
  gh actions-usage billing summary [--group-by account,product,sku,cost-class] [--json]
  gh actions-usage summary [--group-by repo,workflow-path,job,runner-image] [--json]
  gh actions-usage runs list [--json]
  gh actions-usage jobs list [--limit 50] [--json]
  gh actions-usage import --in report.json [--json]
  gh actions-usage serve [--refresh] [--account @me|ORG] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--listen 127.0.0.1:8080] [--open]
  gh actions-usage export --out report.json
  gh actions-usage api get /user
  gh actions-usage cache path|stats|clear

Reports refresh cached Actions data before summarizing it. Billing refreshes use GitHub billing usage APIs. Cached reads use the local SQLite cache.`)
}

func (a *App) cmdDoctor(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "ingest" {
		return a.cmdDoctorIngest(ctx, args[1:])
	}
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
	if os.Getenv("GH_ENTERPRISE_TOKEN") != "" {
		return "env:GH_ENTERPRISE_TOKEN"
	}
	if os.Getenv("GITHUB_ENTERPRISE_TOKEN") != "" {
		return "env:GITHUB_ENTERPRISE_TOKEN"
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

func (a *App) cmdDoctorIngest(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "actions" {
		return fmt.Errorf("usage: doctor ingest actions --account @me|ORG [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD]")
	}
	fs := flag.NewFlagSet("doctor ingest actions", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	account := fs.String("account", "@me", "account to inspect")
	repoFilter := fs.String("repo", "", "comma-separated repositories to refresh")
	since := fs.String("since", "", "start date YYYY-MM-DD")
	until := fs.String("until", "", "end date YYYY-MM-DD")
	days := fs.Int("days", 30, "days back when --since is absent")
	var accountPlans keyValueFlag
	var billingOwners keyValueFlag
	var billingKinds keyValueFlag
	fs.Var(&accountPlans, "account-plan", "comma-separated ACCOUNT=PLAN annotations")
	fs.Var(&billingOwners, "billing-owner", "comma-separated ACCOUNT=BILLING_OWNER overrides")
	fs.Var(&billingKinds, "billing-kind", "comma-separated ACCOUNT=user|org|enterprise overrides")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	result, _, err := a.refreshActions(ctx, IngestOptions{Account: *account, RepoFilter: *repoFilter, Since: *since, Until: *until, Days: *days, DaysSet: flagWasSet(fs, "days"), AccountPlans: accountPlans, BillingOwners: billingOwners, BillingKinds: billingKinds})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "ingested %d repos, %d runs, %d jobs\n", result.ReposIngested, result.RunsUpserted, result.JobsUpserted)
	fmt.Fprintf(a.stdout, "cache: %s\n", result.CachePath)
	return nil
}

func (a *App) cmdReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	account := fs.String("account", "@me", "account to inspect")
	repoFilter := fs.String("repo", "", "comma-separated repositories to include")
	since := fs.String("since", "", "start date YYYY-MM-DD")
	until := fs.String("until", "", "end date YYYY-MM-DD")
	days := fs.Int("days", 30, "days back when --since is absent")
	groupBy := fs.String("group-by", "repo,workflow-path,job,runner-image", "comma-separated dimensions")
	var accountPlans keyValueFlag
	var billingOwners keyValueFlag
	var billingKinds keyValueFlag
	fs.Var(&accountPlans, "account-plan", "comma-separated ACCOUNT=PLAN annotations")
	fs.Var(&billingOwners, "billing-owner", "comma-separated ACCOUNT=BILLING_OWNER overrides")
	fs.Var(&billingKinds, "billing-kind", "comma-separated ACCOUNT=user|org|enterprise overrides")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, selected, err := a.refreshActions(ctx, IngestOptions{Account: *account, RepoFilter: *repoFilter, Since: *since, Until: *until, Days: *days, DaysSet: flagWasSet(fs, "days"), AccountPlans: accountPlans, BillingOwners: billingOwners, BillingKinds: billingKinds})
	if err != nil {
		return err
	}
	filters := reportFilters(*repoFilter, *since, *until, *days, a.now())
	jobs, err := a.cache.ListJobs(QueryFilters{Since: filters.Since, Until: filters.Until})
	if err != nil {
		return err
	}
	jobs = filterJobsByRepos(jobs, selected)
	summary := buildSummary(a.cache.Path(), jobs, filters, splitCSV(*groupBy), a.now())
	report := ReportResult{Refresh: result, Summary: summary}
	if *jsonOut {
		return writeJSON(a.stdout, report)
	}
	fmt.Fprintf(a.stderr, "refreshed %d repos, %d runs, %d jobs\n", result.ReposIngested, result.RunsUpserted, result.JobsUpserted)
	printSummary(a.stdout, summary)
	return nil
}

func (a *App) cmdBilling(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billing refresh|summary")
	}
	switch args[0] {
	case "refresh":
		return a.cmdBillingRefresh(ctx, args[1:])
	case "summary":
		return a.cmdBillingSummary(args[1:])
	default:
		return fmt.Errorf("usage: billing refresh|summary")
	}
}

func (a *App) cmdBillingRefresh(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("billing refresh", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	account := fs.String("account", "@me", "billing account selectors: @me, ORG, org:ORG, user:LOGIN, enterprise:SLUG")
	year := fs.Int("year", 0, "usage year")
	month := fs.Int("month", 0, "usage month")
	day := fs.Int("day", 0, "usage day")
	repo := fs.String("repo", "", "repository filter for supported endpoints")
	product := fs.String("product", "", "product filter for supported endpoints")
	sku := fs.String("sku", "", "SKU filter for supported endpoints")
	organization := fs.String("organization", "", "enterprise organization filter")
	costCenterID := fs.String("cost-center-id", "", "enterprise cost center filter")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if a.client == nil {
		return fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}
	filters := BillingQueryFilters{Year: *year, Month: *month, Day: *day, Repo: *repo, Product: *product, SKU: *sku, Organization: *organization, CostCenterID: *costCenterID}
	accounts, err := a.billingAccountContexts(ctx, *account)
	if err != nil {
		return err
	}
	result := BillingRefreshResult{AccountsRefreshed: len(accounts), CachePath: a.cache.Path()}
	for _, account := range accounts {
		result.Accounts = append(result.Accounts, account.Login)
		records, err := a.fetchBillingUsage(ctx, account, filters)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := a.cache.UpsertBillingUsage(record); err != nil {
				return err
			}
			result.ItemsUpserted++
		}
	}
	if *jsonOut {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "ingested %d billing items across %d accounts\n", result.ItemsUpserted, result.AccountsRefreshed)
	fmt.Fprintf(a.stdout, "cache: %s\n", result.CachePath)
	return nil
}

func (a *App) cmdBillingSummary(args []string) error {
	fs := flag.NewFlagSet("billing summary", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	groupBy := fs.String("group-by", "account,product,sku,cost-class", "comma-separated dimensions")
	account := fs.String("account", "", "billing account filter")
	repo := fs.String("repo", "", "repository filter")
	since := fs.String("since", "", "start date")
	until := fs.String("until", "", "end date")
	product := fs.String("product", "", "product filter")
	sku := fs.String("sku", "", "SKU filter")
	limit := fs.Int("limit", 0, "maximum rows")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	filters := BillingQueryFilters{Account: *account, Repo: *repo, Since: *since, Until: *until, Product: *product, SKU: *sku, Limit: *limit}
	records, err := a.cache.ListBillingUsage(filters)
	if err != nil {
		return err
	}
	summary := buildBillingSummary(a.cache.Path(), records, filters, splitCSV(*groupBy), a.now())
	if *jsonOut {
		return writeJSON(a.stdout, summary)
	}
	printBillingSummary(a.stdout, summary)
	return nil
}

func (a *App) fetchBillingUsage(ctx context.Context, account accountContext, filters BillingQueryFilters) ([]BillingUsageRecord, error) {
	_ = ctx
	endpoint, err := billingUsageEndpoint(account, filters)
	if err != nil {
		return nil, err
	}
	var response struct {
		UsageItems []json.RawMessage `json:"usageItems"`
	}
	if err := a.client.Get(endpoint, &response); err != nil {
		return nil, err
	}
	records := make([]BillingUsageRecord, 0, len(response.UsageItems))
	for _, item := range response.UsageItems {
		record := parseBillingUsageItem(item, account, filters)
		records = append(records, record)
	}
	return records, nil
}

func billingUsageEndpoint(account accountContext, filters BillingQueryFilters) (string, error) {
	var base string
	summary := filters.Repo != "" || filters.Product != "" || filters.SKU != "" || filters.Organization != "" || filters.CostCenterID != ""
	switch account.Kind {
	case "user":
		base = "users/" + url.PathEscape(account.Login) + "/settings/billing/usage"
	case "org":
		base = "organizations/" + url.PathEscape(account.Login) + "/settings/billing/usage"
	case "enterprise":
		base = "enterprises/" + url.PathEscape(account.Login) + "/settings/billing/usage"
	default:
		return "", fmt.Errorf("unsupported billing account kind %q", account.Kind)
	}
	if summary {
		base += "/summary"
	}
	params := orderedQueryParams{
		{"year", strconv.Itoa(filters.Year), filters.Year != 0},
		{"month", strconv.Itoa(filters.Month), filters.Month != 0},
		{"day", strconv.Itoa(filters.Day), filters.Day != 0},
		{"organization", filters.Organization, filters.Organization != ""},
		{"repository", filters.Repo, filters.Repo != ""},
		{"product", filters.Product, filters.Product != ""},
		{"sku", filters.SKU, filters.SKU != ""},
		{"cost_center_id", filters.CostCenterID, filters.CostCenterID != ""},
	}
	return appendOrderedQuery(base, params), nil
}

type orderedQueryParam struct {
	Key     string
	Value   string
	Include bool
}

type orderedQueryParams []orderedQueryParam

func appendOrderedQuery(base string, params orderedQueryParams) string {
	values := make([]string, 0, len(params))
	for _, param := range params {
		if !param.Include {
			continue
		}
		values = append(values, url.QueryEscape(param.Key)+"="+url.QueryEscape(param.Value))
	}
	if len(values) == 0 {
		return base
	}
	return base + "?" + strings.Join(values, "&")
}

func parseBillingUsageItem(data json.RawMessage, account accountContext, filters BillingQueryFilters) BillingUsageRecord {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	record := BillingUsageRecord{
		Account:          account.Login,
		AccountKind:      account.Kind,
		Date:             rawString(raw, "date"),
		Year:             filters.Year,
		Month:            filters.Month,
		Day:              filters.Day,
		Product:          rawString(raw, "product", "productName"),
		SKU:              rawString(raw, "sku", "skuName"),
		UnitType:         rawString(raw, "unitType", "unit_type"),
		Model:            rawString(raw, "model"),
		OrganizationName: rawString(raw, "organizationName", "organization_name", "organization"),
		RepositoryName:   rawString(raw, "repositoryName", "repository_name", "repository"),
		CostCenterID:     rawString(raw, "costCenterId", "costCenterID", "cost_center_id"),
		Quantity:         rawFloat(raw, "quantity"),
		GrossQuantity:    rawFloat(raw, "grossQuantity", "gross_quantity"),
		DiscountQuantity: rawFloat(raw, "discountQuantity", "discount_quantity"),
		NetQuantity:      rawFloat(raw, "netQuantity", "net_quantity"),
		PricePerUnit:     rawFloat(raw, "pricePerUnit", "price_per_unit"),
		GrossAmount:      rawFloat(raw, "grossAmount", "gross_amount"),
		DiscountAmount:   rawFloat(raw, "discountAmount", "discount_amount"),
		NetAmount:        rawFloat(raw, "netAmount", "net_amount"),
		Raw:              append(json.RawMessage(nil), data...),
	}
	if record.Year == 0 || record.Month == 0 || record.Day == 0 {
		year, month, day := dateParts(record.Date)
		if record.Year == 0 {
			record.Year = year
		}
		if record.Month == 0 {
			record.Month = month
		}
		if record.Day == 0 {
			record.Day = day
		}
	}
	if record.GrossQuantity == 0 {
		record.GrossQuantity = record.Quantity
	}
	if record.NetQuantity == 0 && record.DiscountQuantity == 0 {
		record.NetQuantity = record.Quantity
	}
	record.CostClass = billingCostClass(record)
	record.Key = billingUsageKey(record)
	return record
}

func rawString(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if data, ok := raw[key]; ok {
			var value string
			if err := json.Unmarshal(data, &value); err == nil {
				return value
			}
			var number json.Number
			if err := json.Unmarshal(data, &number); err == nil {
				return number.String()
			}
		}
	}
	return ""
}

func rawFloat(raw map[string]json.RawMessage, keys ...string) float64 {
	for _, key := range keys {
		data, ok := raw[key]
		if !ok {
			continue
		}
		var value float64
		if err := json.Unmarshal(data, &value); err == nil {
			return value
		}
		var text string
		if err := json.Unmarshal(data, &text); err == nil {
			parsed, _ := strconv.ParseFloat(text, 64)
			return parsed
		}
	}
	return 0
}

func dateParts(value string) (int, int, int) {
	if value == "" {
		return 0, 0, 0
	}
	t, err := time.Parse(dateFormat, dateOnly(value))
	if err != nil {
		return 0, 0, 0
	}
	return t.Year(), int(t.Month()), t.Day()
}

func billingCostClass(record BillingUsageRecord) string {
	switch {
	case record.NetAmount > 0 && record.DiscountAmount > 0:
		return "discounted"
	case record.NetAmount > 0:
		return "paid"
	case record.NetAmount == 0 && record.DiscountAmount > 0:
		return "discounted"
	case record.GrossAmount == 0 && record.NetAmount == 0:
		return "free"
	default:
		return "unknown"
	}
}

func billingUsageKey(record BillingUsageRecord) string {
	parts := []string{
		record.AccountKind,
		record.Account,
		record.Date,
		strconv.Itoa(record.Year),
		strconv.Itoa(record.Month),
		strconv.Itoa(record.Day),
		record.Product,
		record.SKU,
		record.Model,
		record.UnitType,
		record.OrganizationName,
		record.RepositoryName,
		record.CostCenterID,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum)
}

func (a *App) refreshActions(ctx context.Context, options IngestOptions) (IngestResult, []Repo, error) {
	if a.client == nil {
		return IngestResult{}, nil, fmt.Errorf("GitHub API client unavailable; run `gh auth login`")
	}
	if options.Account == "" {
		options.Account = "@me"
	}
	if options.Days == 0 && !options.DaysSet {
		options.Days = 30
	}
	created, err := createdQuery(options.Since, options.Until, options.Days, a.now())
	if err != nil {
		return IngestResult{}, nil, err
	}

	accounts, err := a.actionAccountContexts(ctx, options.Account)
	if err != nil {
		return IngestResult{}, nil, err
	}
	result := IngestResult{Account: options.Account, AccountsIngested: len(accounts), CachePath: a.cache.Path()}
	selectedByRepo := map[string]Repo{}
	for _, account := range accounts {
		repos, err := a.fetchReposForAccount(ctx, account)
		if err != nil {
			return IngestResult{}, nil, err
		}
		result.ReposSeen += len(repos)
		for _, repo := range filterRepos(repos, options.RepoFilter) {
			repo = annotateRepo(repo, account, options)
			if _, seen := selectedByRepo[repo.FullName]; seen {
				continue
			}
			selectedByRepo[repo.FullName] = repo
		}
	}
	selected := make([]Repo, 0, len(selectedByRepo))
	for _, repo := range selectedByRepo {
		selected = append(selected, repo)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].FullName < selected[j].FullName })
	if len(selected) == 0 {
		return IngestResult{}, nil, fmt.Errorf("no repositories matched")
	}
	result.ReposIngested = len(selected)
	for _, repo := range selected {
		if err := a.cache.UpsertRepo(repo); err != nil {
			return IngestResult{}, nil, err
		}
		runs, err := a.fetchRuns(ctx, repo, created)
		if err != nil {
			return IngestResult{}, nil, err
		}
		workflowPaths := map[int64]string{}
		for _, run := range runs {
			if _, ok := workflowPaths[run.WorkflowID]; !ok && run.WorkflowID != 0 {
				workflowPaths[run.WorkflowID] = a.fetchWorkflowPath(repo, run.WorkflowID)
			}
			run.WorkflowPath = workflowPaths[run.WorkflowID]
			run = annotateRun(run, repo)
			if err := a.cache.UpsertRun(run); err != nil {
				return IngestResult{}, nil, err
			}
			result.RunsUpserted++

			jobs, err := a.fetchJobs(ctx, repo, run)
			if err != nil {
				return IngestResult{}, nil, err
			}
			for _, job := range jobs {
				job = annotateJob(job, repo)
				if err := a.cache.UpsertJob(job); err != nil {
					return IngestResult{}, nil, err
				}
				result.JobsUpserted++
			}
		}
	}
	return result, selected, nil
}

type accountContext struct {
	Selector string
	Login    string
	Kind     string
}

func (a *App) actionAccountContexts(ctx context.Context, raw string) ([]accountContext, error) {
	_ = ctx
	selectors := splitCSV(raw)
	if len(selectors) == 0 {
		selectors = []string{"@me"}
	}
	var accounts []accountContext
	for _, selector := range selectors {
		kind, login := parseAccountSelector(selector)
		switch kind {
		case "user":
			if selector != "@me" {
				return nil, fmt.Errorf("Actions refresh can inspect @me or organizations; got %q", selector)
			}
			resolved, err := a.currentUserLogin()
			if err != nil {
				return nil, err
			}
			accounts = append(accounts, accountContext{Selector: selector, Login: resolved, Kind: "user"})
		case "org":
			accounts = append(accounts, accountContext{Selector: selector, Login: login, Kind: "org"})
		default:
			return nil, fmt.Errorf("Actions refresh does not support %s accounts", kind)
		}
	}
	return accounts, nil
}

func (a *App) billingAccountContexts(ctx context.Context, raw string) ([]accountContext, error) {
	_ = ctx
	selectors := splitCSV(raw)
	if len(selectors) == 0 {
		selectors = []string{"@me"}
	}
	var accounts []accountContext
	for _, selector := range selectors {
		kind, login := parseAccountSelector(selector)
		if selector == "@me" {
			resolved, err := a.currentUserLogin()
			if err != nil {
				return nil, err
			}
			login = resolved
		}
		accounts = append(accounts, accountContext{Selector: selector, Login: login, Kind: kind})
	}
	return accounts, nil
}

func parseAccountSelector(selector string) (string, string) {
	selector = strings.TrimSpace(selector)
	switch {
	case selector == "" || selector == "@me":
		return "user", "@me"
	case strings.Contains(selector, ":"):
		prefix, login, _ := strings.Cut(selector, ":")
		return normalizeAccountKind(prefix), strings.TrimSpace(login)
	case strings.Contains(selector, "/"):
		prefix, login, _ := strings.Cut(selector, "/")
		return normalizeAccountKind(prefix), strings.TrimSpace(login)
	default:
		return "org", selector
	}
}

func (a *App) currentUserLogin() (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := a.client.Get("user", &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", fmt.Errorf("GitHub API did not return a user login")
	}
	return user.Login, nil
}

func annotateRepo(repo Repo, account accountContext, options IngestOptions) Repo {
	repo.Account = account.Login
	repo.OwnerKind = firstNonEmpty(normalizeAccountKind(repo.OwnerKind), inferRepoOwnerKind(repo, account), "unknown")
	repo.BillingOwner = firstNonEmpty(lookupOverride(options.BillingOwners, account.Selector, account.Login, repo.Owner, repo.FullName), account.Login)
	repo.BillingOwnerKind = firstNonEmpty(lookupOverride(options.BillingKinds, account.Selector, account.Login, repo.Owner, repo.FullName), account.Kind)
	repo.BillingPlan = lookupOverride(options.AccountPlans, account.Selector, account.Login, repo.Owner, repo.BillingOwner, repo.FullName)
	return repo
}

func annotateRun(run RunRecord, repo Repo) RunRecord {
	run.Account = repo.Account
	run.RepoOwner = repo.Owner
	run.RepoOwnerKind = repo.OwnerKind
	run.BillingOwner = repo.BillingOwner
	run.BillingOwnerKind = repo.BillingOwnerKind
	run.BillingPlan = repo.BillingPlan
	return run
}

func annotateJob(job JobRecord, repo Repo) JobRecord {
	job.Account = repo.Account
	job.RepoOwner = repo.Owner
	job.RepoOwnerKind = repo.OwnerKind
	job.BillingOwner = repo.BillingOwner
	job.BillingOwnerKind = repo.BillingOwnerKind
	job.BillingPlan = repo.BillingPlan
	job.CostClass = actionCostClass(repo, job)
	return job
}

func inferRepoOwnerKind(repo Repo, account accountContext) string {
	if repo.Owner != "" && strings.EqualFold(repo.Owner, account.Login) {
		return account.Kind
	}
	return ""
}

func actionCostClass(repo Repo, job JobRecord) string {
	if strings.EqualFold(job.Runner.Type, "self-hosted") {
		return "external"
	}
	if !repo.Private {
		return "free"
	}
	if repo.BillingOwnerKind == "enterprise" || strings.Contains(strings.ToLower(repo.BillingPlan), "enterprise") {
		return "enterprise"
	}
	if repo.BillingPlan != "" {
		return "paid"
	}
	return "unknown"
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
	billing, err := a.cache.ListBillingUsage(BillingQueryFilters{})
	if err != nil {
		return err
	}
	payload := ExportPayload{Runs: runs, Jobs: jobs, Repos: repos, BillingUsage: billing, ExportedAt: a.now().Format(time.RFC3339)}
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
	if len(billing) > 0 {
		fmt.Fprintf(a.stdout, "exported %d repos, %d runs, %d jobs, and %d billing rows to %s\n", len(repos), len(runs), len(jobs), len(billing), *out)
		return nil
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
	for _, record := range payload.BillingUsage {
		if err := a.cache.UpsertBillingUsage(record); err != nil {
			return err
		}
		result.BillingImported++
	}
	if *jsonOut {
		return writeJSON(a.stdout, result)
	}
	if result.BillingImported > 0 {
		fmt.Fprintf(a.stdout, "imported %d repos, %d runs, %d jobs, %d billing rows\n", result.ReposImported, result.RunsImported, result.JobsImported, result.BillingImported)
	} else {
		fmt.Fprintf(a.stdout, "imported %d repos, %d runs, %d jobs\n", result.ReposImported, result.RunsImported, result.JobsImported)
	}
	fmt.Fprintf(a.stdout, "cache: %s\n", result.CachePath)
	return nil
}

func (a *App) cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	listen := fs.String("listen", "127.0.0.1:8080", "listen address")
	openBrowser := fs.Bool("open", false, "open browser")
	refresh := fs.Bool("refresh", false, "refresh Actions data before serving")
	account := fs.String("account", "@me", "account to inspect when refreshing")
	repoFilter := fs.String("repo", "", "comma-separated repositories to refresh")
	since := fs.String("since", "", "start date YYYY-MM-DD")
	until := fs.String("until", "", "end date YYYY-MM-DD")
	days := fs.Int("days", 30, "days back when --since is absent")
	var accountPlans keyValueFlag
	var billingOwners keyValueFlag
	var billingKinds keyValueFlag
	fs.Var(&accountPlans, "account-plan", "comma-separated ACCOUNT=PLAN annotations")
	fs.Var(&billingOwners, "billing-owner", "comma-separated ACCOUNT=BILLING_OWNER overrides")
	fs.Var(&billingKinds, "billing-kind", "comma-separated ACCOUNT=user|org|enterprise overrides")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *refresh {
		result, selected, err := a.refreshActions(ctx, IngestOptions{Account: *account, RepoFilter: *repoFilter, Since: *since, Until: *until, Days: *days, DaysSet: flagWasSet(fs, "days"), AccountPlans: accountPlans, BillingOwners: billingOwners, BillingKinds: billingKinds})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stderr, "refreshed %d repos, %d runs, %d jobs\n", result.ReposIngested, result.RunsUpserted, result.JobsUpserted)
		return a.serveWithScope(WebScope{Filters: reportFilters(*repoFilter, *since, *until, *days, a.now()), Repos: selected}, *listen, *openBrowser)
	}
	return a.serveWithScope(WebScope{}, *listen, *openBrowser)
}

func (a *App) serveWithScope(scope WebScope, listen string, openBrowser bool) error {
	handler, err := a.webHandlerWithScope(scope)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	addr := "http://" + ln.Addr().String()
	fmt.Fprintf(a.stdout, "serving %s\n", addr)
	if openBrowser {
		_ = browseURLFunc(addr, a.stdout, a.stderr)
	}
	return serveHTTPFunc(ln, handler)
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
	if hasHelpArg(args) {
		fmt.Fprintln(a.stdout, "usage: cache path|stats|clear")
		return nil
	}
	if len(args) != 1 {
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
		login, err := a.currentUserLogin()
		if err != nil {
			return nil, err
		}
		return a.fetchReposForAccount(ctx, accountContext{Selector: "@me", Login: login, Kind: "user"})
	}
	return a.fetchReposForAccount(ctx, accountContext{Selector: account, Login: account, Kind: "org"})
}

func (a *App) fetchReposForAccount(ctx context.Context, account accountContext) ([]Repo, error) {
	_ = ctx
	switch account.Kind {
	case "user":
		return fetchPaged[Repo](a.client, "user/repos?visibility=all&affiliation=owner&sort=full_name")
	case "org":
		return fetchPaged[Repo](a.client, "orgs/"+url.PathEscape(account.Login)+"/repos?type=all&sort=full_name")
	default:
		return nil, fmt.Errorf("repository listing does not support %s accounts", account.Kind)
	}
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
	return filepath.Join(xdgCacheHome(), appName, "cache.db")
}

func xdgCacheHome() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" && filepath.IsAbs(dir) {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".cache")
	}
	return filepath.Join(home, ".cache")
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

func filterJobsByRepos(jobs []JobRecord, repos []Repo) []JobRecord {
	if len(repos) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, repo := range repos {
		allowed[repo.FullName] = true
	}
	out := jobs[:0]
	for _, job := range jobs {
		if allowed[job.Repo] {
			out = append(out, job)
		}
	}
	return out
}

func reportFilters(repo string, since string, until string, days int, now time.Time) QueryFilters {
	if since == "" && until == "" {
		since = now.AddDate(0, 0, -days).Format(dateFormat)
	}
	return QueryFilters{Repo: repo, Since: since, Until: until}
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

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

type keyValueFlag map[string]string

func (f keyValueFlag) String() string {
	if len(f) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+f[key])
	}
	return strings.Join(parts, ",")
}

func (f *keyValueFlag) Set(value string) error {
	if *f == nil {
		*f = keyValueFlag{}
	}
	for _, part := range splitCSV(value) {
		key, val, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("expected KEY=VALUE, got %q", part)
		}
		(*f)[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return nil
}

func lookupOverride(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value := values[key]; value != "" {
			return value
		}
	}
	return ""
}

func normalizeAccountKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "user", "personal":
		return "user"
	case "org", "organization":
		return "org"
	case "enterprise", "business":
		return "enterprise"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
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
	groupRuns := map[string]map[int64]bool{}
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
			groupRuns[key] = map[int64]bool{}
		}
		group.Jobs++
		groupRuns[key][job.RunID] = true
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
		group.Runs = len(groupRuns[group.Key])
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

func buildBillingSummary(cachePath string, records []BillingUsageRecord, filters BillingQueryFilters, groupBy []string, now time.Time) BillingSummary {
	summary := BillingSummary{
		GeneratedAt: now.Format(time.RFC3339),
		CachePath:   cachePath,
		Filters:     filters,
		GroupBy:     groupBy,
		TotalItems:  len(records),
	}
	for _, record := range records {
		summary.GrossQuantity += record.GrossQuantity
		summary.DiscountQuantity += record.DiscountQuantity
		summary.NetQuantity += record.NetQuantity
		summary.GrossAmount += record.GrossAmount
		summary.DiscountAmount += record.DiscountAmount
		summary.NetAmount += record.NetAmount
	}
	summary.Groups = summarizeBilling(records, groupBy)
	return summary
}

func summarizeBilling(records []BillingUsageRecord, groupBy []string) []BillingSummaryGroup {
	if len(groupBy) == 0 {
		return nil
	}
	groups := map[string]*BillingSummaryGroup{}
	for _, record := range records {
		values := map[string]string{}
		keyParts := make([]string, 0, len(groupBy))
		for _, dim := range groupBy {
			value := billingDimension(record, dim)
			values[dim] = value
			keyParts = append(keyParts, value)
		}
		key := strings.Join(keyParts, "\t")
		group := groups[key]
		if group == nil {
			group = &BillingSummaryGroup{Key: key, Values: values}
			groups[key] = group
		}
		group.Items++
		group.GrossQuantity += record.GrossQuantity
		group.DiscountQuantity += record.DiscountQuantity
		group.NetQuantity += record.NetQuantity
		group.GrossAmount += record.GrossAmount
		group.DiscountAmount += record.DiscountAmount
		group.NetAmount += record.NetAmount
	}
	out := make([]BillingSummaryGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NetAmount == out[j].NetAmount {
			return out[i].Key < out[j].Key
		}
		return out[i].NetAmount > out[j].NetAmount
	})
	return out
}

func dimension(job JobRecord, dim string) string {
	switch dim {
	case "account":
		return firstNonEmpty(job.Account, "unknown")
	case "date":
		return dateOnly(job.StartedAt)
	case "repo":
		return firstNonEmpty(job.Repo, "unknown")
	case "repo-owner":
		return firstNonEmpty(job.RepoOwner, ownerFromRepo(job.Repo), "unknown")
	case "repo-owner-kind":
		return firstNonEmpty(job.RepoOwnerKind, "unknown")
	case "billing-owner":
		return firstNonEmpty(job.BillingOwner, job.RepoOwner, ownerFromRepo(job.Repo), "unknown")
	case "billing-owner-kind":
		return firstNonEmpty(job.BillingOwnerKind, "unknown")
	case "billing-plan", "plan":
		return firstNonEmpty(job.BillingPlan, "unknown")
	case "cost-class":
		return firstNonEmpty(job.CostClass, "unknown")
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

func billingDimension(record BillingUsageRecord, dim string) string {
	switch dim {
	case "account":
		return firstNonEmpty(record.Account, "unknown")
	case "account-kind":
		return firstNonEmpty(record.AccountKind, "unknown")
	case "date":
		return firstNonEmpty(record.Date, "unknown")
	case "year":
		if record.Year == 0 {
			return "unknown"
		}
		return strconv.Itoa(record.Year)
	case "month":
		if record.Month == 0 {
			return "unknown"
		}
		return fmt.Sprintf("%04d-%02d", record.Year, record.Month)
	case "product":
		return firstNonEmpty(record.Product, "unknown")
	case "sku":
		return firstNonEmpty(record.SKU, "unknown")
	case "unit", "unit-type":
		return firstNonEmpty(record.UnitType, "unknown")
	case "model":
		return firstNonEmpty(record.Model, "unknown")
	case "org", "organization":
		return firstNonEmpty(record.OrganizationName, "unknown")
	case "repo", "repository":
		return firstNonEmpty(record.RepositoryName, "unknown")
	case "cost-center", "cost-center-id":
		return firstNonEmpty(record.CostCenterID, "unknown")
	case "cost-class":
		return firstNonEmpty(record.CostClass, "unknown")
	default:
		return "unknown"
	}
}

func ownerFromRepo(repo string) string {
	owner, _, ok := strings.Cut(repo, "/")
	if !ok {
		return ""
	}
	return owner
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

func printBillingSummary(w io.Writer, summary BillingSummary) {
	fmt.Fprintf(w, "items: %d\n", summary.TotalItems)
	fmt.Fprintf(w, "gross: $%.2f\n", summary.GrossAmount)
	fmt.Fprintf(w, "discount: $%.2f\n", summary.DiscountAmount)
	fmt.Fprintf(w, "net: $%.2f\n", summary.NetAmount)
	if len(summary.Groups) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := append([]string{}, summary.GroupBy...)
	header = append(header, "items", "gross", "discount", "net")
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, group := range summary.Groups {
		row := make([]string, 0, len(summary.GroupBy)+4)
		for _, dim := range summary.GroupBy {
			row = append(row, group.Values[dim])
		}
		row = append(row, strconv.Itoa(group.Items), fmt.Sprintf("$%.2f", group.GrossAmount), fmt.Sprintf("$%.2f", group.DiscountAmount), fmt.Sprintf("$%.2f", group.NetAmount))
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
		`pragma journal_mode = wal`,
		`pragma busy_timeout = 5000`,
		`create table if not exists repos (
			full_name text primary key,
			id integer,
			account text,
			owner text,
			owner_kind text,
			name text,
			private integer,
			billing_owner text,
			billing_owner_kind text,
			billing_plan text,
			raw_json text,
			updated_at text default current_timestamp
		)`,
		`create table if not exists runs (
			id integer primary key,
			account text,
			repo text,
			repo_owner text,
			repo_owner_kind text,
			billing_owner text,
			billing_owner_kind text,
			billing_plan text,
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
			account text,
			repo text,
			repo_owner text,
			repo_owner_kind text,
			billing_owner text,
			billing_owner_kind text,
			billing_plan text,
			cost_class text,
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
		`create table if not exists billing_usage (
			key text primary key,
			account text,
			account_kind text,
			date text,
			year integer,
			month integer,
			day integer,
			product text,
			sku text,
			unit_type text,
			model text,
			organization_name text,
			repository_name text,
			cost_center_id text,
			cost_class text,
			quantity real,
			gross_quantity real,
			discount_quantity real,
			net_quantity real,
			price_per_unit real,
			gross_amount real,
			discount_amount real,
			net_amount real,
			raw_json text,
			updated_at text default current_timestamp
		)`,
		`create index if not exists idx_jobs_repo_started on jobs(repo, started_at)`,
		`create index if not exists idx_runs_repo_started on runs(repo, run_started_at)`,
		`create index if not exists idx_billing_usage_account_date on billing_usage(account, date)`,
	}
	for _, stmt := range statements {
		if _, err := c.db.Exec(stmt); err != nil {
			return err
		}
	}
	return c.ensureColumns()
}

func (c *Cache) ensureColumns() error {
	columns := []struct {
		table string
		def   string
	}{
		{"repos", "account text"},
		{"repos", "owner_kind text"},
		{"repos", "billing_owner text"},
		{"repos", "billing_owner_kind text"},
		{"repos", "billing_plan text"},
		{"runs", "account text"},
		{"runs", "repo_owner text"},
		{"runs", "repo_owner_kind text"},
		{"runs", "billing_owner text"},
		{"runs", "billing_owner_kind text"},
		{"runs", "billing_plan text"},
		{"jobs", "account text"},
		{"jobs", "repo_owner text"},
		{"jobs", "repo_owner_kind text"},
		{"jobs", "billing_owner text"},
		{"jobs", "billing_owner_kind text"},
		{"jobs", "billing_plan text"},
		{"jobs", "cost_class text"},
	}
	for _, column := range columns {
		if _, err := c.db.Exec("alter table " + column.table + " add column " + column.def); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (c *Cache) UpsertRepo(repo Repo) error {
	raw, _ := json.Marshal(repo)
	_, err := c.db.Exec(`insert into repos(full_name,id,account,owner,owner_kind,name,private,billing_owner,billing_owner_kind,billing_plan,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(full_name) do update set id=excluded.id, account=excluded.account, owner=excluded.owner, owner_kind=excluded.owner_kind, name=excluded.name, private=excluded.private, billing_owner=excluded.billing_owner, billing_owner_kind=excluded.billing_owner_kind, billing_plan=excluded.billing_plan, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		repo.FullName, repo.ID, repo.Account, repo.Owner, repo.OwnerKind, repo.Name, boolInt(repo.Private), repo.BillingOwner, repo.BillingOwnerKind, repo.BillingPlan, string(firstRaw(repo.Raw, raw)))
	return err
}

func (c *Cache) UpsertRun(run RunRecord) error {
	raw, _ := json.Marshal(run)
	_, err := c.db.Exec(`insert into runs(id,account,repo,repo_owner,repo_owner_kind,billing_owner,billing_owner_kind,billing_plan,workflow_id,workflow_name,workflow_path,run_number,run_attempt,event,branch,actor,status,conclusion,created_at,run_started_at,html_url,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(id) do update set account=excluded.account, repo=excluded.repo, repo_owner=excluded.repo_owner, repo_owner_kind=excluded.repo_owner_kind, billing_owner=excluded.billing_owner, billing_owner_kind=excluded.billing_owner_kind, billing_plan=excluded.billing_plan, workflow_id=excluded.workflow_id, workflow_name=excluded.workflow_name, workflow_path=excluded.workflow_path, run_number=excluded.run_number, run_attempt=excluded.run_attempt, event=excluded.event, branch=excluded.branch, actor=excluded.actor, status=excluded.status, conclusion=excluded.conclusion, created_at=excluded.created_at, run_started_at=excluded.run_started_at, html_url=excluded.html_url, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		run.ID, run.Account, run.Repo, run.RepoOwner, run.RepoOwnerKind, run.BillingOwner, run.BillingOwnerKind, run.BillingPlan, run.WorkflowID, run.WorkflowName, run.WorkflowPath, run.RunNumber, run.RunAttempt, run.Event, run.Branch, run.Actor, run.Status, run.Conclusion, run.CreatedAt, run.RunStartedAt, run.HTMLURL, string(firstRaw(run.Raw, raw)))
	return err
}

func (c *Cache) UpsertJob(job JobRecord) error {
	labels, _ := json.Marshal(job.Labels)
	raw, _ := json.Marshal(job)
	_, err := c.db.Exec(`insert into jobs(id,run_id,account,repo,repo_owner,repo_owner_kind,billing_owner,billing_owner_kind,billing_plan,cost_class,workflow_name,workflow_path,name,status,conclusion,started_at,completed_at,duration_seconds,runner_name,runner_group,runner_type,runner_os,runner_arch,runner_image,labels_json,html_url,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(id) do update set run_id=excluded.run_id, account=excluded.account, repo=excluded.repo, repo_owner=excluded.repo_owner, repo_owner_kind=excluded.repo_owner_kind, billing_owner=excluded.billing_owner, billing_owner_kind=excluded.billing_owner_kind, billing_plan=excluded.billing_plan, cost_class=excluded.cost_class, workflow_name=excluded.workflow_name, workflow_path=excluded.workflow_path, name=excluded.name, status=excluded.status, conclusion=excluded.conclusion, started_at=excluded.started_at, completed_at=excluded.completed_at, duration_seconds=excluded.duration_seconds, runner_name=excluded.runner_name, runner_group=excluded.runner_group, runner_type=excluded.runner_type, runner_os=excluded.runner_os, runner_arch=excluded.runner_arch, runner_image=excluded.runner_image, labels_json=excluded.labels_json, html_url=excluded.html_url, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		job.ID, job.RunID, job.Account, job.Repo, job.RepoOwner, job.RepoOwnerKind, job.BillingOwner, job.BillingOwnerKind, job.BillingPlan, job.CostClass, job.WorkflowName, job.WorkflowPath, job.Name, job.Status, job.Conclusion, job.StartedAt, job.CompletedAt, job.DurationSecs, job.RunnerName, job.RunnerGroup, job.Runner.Type, job.Runner.OS, job.Runner.Arch, job.Runner.Image, string(labels), job.HTMLURL, string(firstRaw(job.Raw, raw)))
	return err
}

func (c *Cache) ListRuns(filters QueryFilters) ([]RunRecord, error) {
	query := `select id,coalesce(account,''),repo,coalesce(repo_owner,''),coalesce(repo_owner_kind,''),coalesce(billing_owner,''),coalesce(billing_owner_kind,''),coalesce(billing_plan,''),workflow_id,workflow_name,workflow_path,run_number,run_attempt,event,branch,actor,status,conclusion,created_at,run_started_at,html_url,coalesce(raw_json,'') from runs`
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
		if err := rows.Scan(&run.ID, &run.Account, &run.Repo, &run.RepoOwner, &run.RepoOwnerKind, &run.BillingOwner, &run.BillingOwnerKind, &run.BillingPlan, &run.WorkflowID, &run.WorkflowName, &run.WorkflowPath, &run.RunNumber, &run.RunAttempt, &run.Event, &run.Branch, &run.Actor, &run.Status, &run.Conclusion, &run.CreatedAt, &run.RunStartedAt, &run.HTMLURL, &raw); err != nil {
			return nil, err
		}
		run.Raw = json.RawMessage(raw)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (c *Cache) ListRepos() ([]Repo, error) {
	rows, err := c.db.Query(`select id,coalesce(account,''),owner,coalesce(owner_kind,''),name,full_name,private,coalesce(billing_owner,''),coalesce(billing_owner_kind,''),coalesce(billing_plan,''),coalesce(raw_json,'') from repos order by full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var repos []Repo
	for rows.Next() {
		var repo Repo
		var private int
		var raw string
		if err := rows.Scan(&repo.ID, &repo.Account, &repo.Owner, &repo.OwnerKind, &repo.Name, &repo.FullName, &private, &repo.BillingOwner, &repo.BillingOwnerKind, &repo.BillingPlan, &raw); err != nil {
			return nil, err
		}
		repo.Private = private == 1
		repo.Raw = json.RawMessage(raw)
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (c *Cache) ListJobs(filters QueryFilters) ([]JobRecord, error) {
	query := `select id,run_id,coalesce(account,''),repo,coalesce(repo_owner,''),coalesce(repo_owner_kind,''),coalesce(billing_owner,''),coalesce(billing_owner_kind,''),coalesce(billing_plan,''),coalesce(cost_class,''),workflow_name,workflow_path,name,status,conclusion,started_at,completed_at,duration_seconds,runner_name,runner_group,runner_type,runner_os,runner_arch,runner_image,coalesce(labels_json,'[]'),html_url,coalesce(raw_json,'') from jobs`
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
		if err := rows.Scan(&job.ID, &job.RunID, &job.Account, &job.Repo, &job.RepoOwner, &job.RepoOwnerKind, &job.BillingOwner, &job.BillingOwnerKind, &job.BillingPlan, &job.CostClass, &job.WorkflowName, &job.WorkflowPath, &job.Name, &job.Status, &job.Conclusion, &job.StartedAt, &job.CompletedAt, &job.DurationSecs, &job.RunnerName, &job.RunnerGroup, &job.Runner.Type, &job.Runner.OS, &job.Runner.Arch, &job.Runner.Image, &labels, &job.HTMLURL, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &job.Labels)
		job.Raw = json.RawMessage(raw)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (c *Cache) UpsertBillingUsage(record BillingUsageRecord) error {
	raw, _ := json.Marshal(record)
	_, err := c.db.Exec(`insert into billing_usage(key,account,account_kind,date,year,month,day,product,sku,unit_type,model,organization_name,repository_name,cost_center_id,cost_class,quantity,gross_quantity,discount_quantity,net_quantity,price_per_unit,gross_amount,discount_amount,net_amount,raw_json,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,current_timestamp)
		on conflict(key) do update set account=excluded.account, account_kind=excluded.account_kind, date=excluded.date, year=excluded.year, month=excluded.month, day=excluded.day, product=excluded.product, sku=excluded.sku, unit_type=excluded.unit_type, model=excluded.model, organization_name=excluded.organization_name, repository_name=excluded.repository_name, cost_center_id=excluded.cost_center_id, cost_class=excluded.cost_class, quantity=excluded.quantity, gross_quantity=excluded.gross_quantity, discount_quantity=excluded.discount_quantity, net_quantity=excluded.net_quantity, price_per_unit=excluded.price_per_unit, gross_amount=excluded.gross_amount, discount_amount=excluded.discount_amount, net_amount=excluded.net_amount, raw_json=excluded.raw_json, updated_at=current_timestamp`,
		record.Key, record.Account, record.AccountKind, record.Date, record.Year, record.Month, record.Day, record.Product, record.SKU, record.UnitType, record.Model, record.OrganizationName, record.RepositoryName, record.CostCenterID, record.CostClass, record.Quantity, record.GrossQuantity, record.DiscountQuantity, record.NetQuantity, record.PricePerUnit, record.GrossAmount, record.DiscountAmount, record.NetAmount, string(firstRaw(record.Raw, raw)))
	return err
}

func (c *Cache) ListBillingUsage(filters BillingQueryFilters) ([]BillingUsageRecord, error) {
	query := `select key,account,account_kind,coalesce(date,''),coalesce(year,0),coalesce(month,0),coalesce(day,0),coalesce(product,''),coalesce(sku,''),coalesce(unit_type,''),coalesce(model,''),coalesce(organization_name,''),coalesce(repository_name,''),coalesce(cost_center_id,''),coalesce(cost_class,''),coalesce(quantity,0),coalesce(gross_quantity,0),coalesce(discount_quantity,0),coalesce(net_quantity,0),coalesce(price_per_unit,0),coalesce(gross_amount,0),coalesce(discount_amount,0),coalesce(net_amount,0),coalesce(raw_json,'') from billing_usage`
	where, args := billingFiltersWhere(filters)
	if where != "" {
		query += " where " + where
	}
	query += " order by net_amount desc, date desc"
	if filters.Limit > 0 {
		query += fmt.Sprintf(" limit %d", filters.Limit)
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []BillingUsageRecord
	for rows.Next() {
		var record BillingUsageRecord
		var raw string
		if err := rows.Scan(&record.Key, &record.Account, &record.AccountKind, &record.Date, &record.Year, &record.Month, &record.Day, &record.Product, &record.SKU, &record.UnitType, &record.Model, &record.OrganizationName, &record.RepositoryName, &record.CostCenterID, &record.CostClass, &record.Quantity, &record.GrossQuantity, &record.DiscountQuantity, &record.NetQuantity, &record.PricePerUnit, &record.GrossAmount, &record.DiscountAmount, &record.NetAmount, &raw); err != nil {
			return nil, err
		}
		record.Raw = json.RawMessage(raw)
		records = append(records, record)
	}
	return records, rows.Err()
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

func billingFiltersWhere(filters BillingQueryFilters) (string, []any) {
	var parts []string
	var args []any
	if filters.Account != "" {
		parts = append(parts, "account = ?")
		args = append(args, filters.Account)
	}
	if filters.Repo != "" {
		parts = append(parts, "repository_name = ?")
		args = append(args, filters.Repo)
	}
	if filters.Since != "" {
		parts = append(parts, "date >= ?")
		args = append(args, filters.Since)
	}
	if filters.Until != "" {
		parts = append(parts, "date <= ?")
		args = append(args, filters.Until)
	}
	if filters.Year != 0 {
		parts = append(parts, "year = ?")
		args = append(args, filters.Year)
	}
	if filters.Month != 0 {
		parts = append(parts, "month = ?")
		args = append(args, filters.Month)
	}
	if filters.Day != 0 {
		parts = append(parts, "day = ?")
		args = append(args, filters.Day)
	}
	if filters.Product != "" {
		parts = append(parts, "product = ?")
		args = append(args, filters.Product)
	}
	if filters.SKU != "" {
		parts = append(parts, "sku = ?")
		args = append(args, filters.SKU)
	}
	if filters.Organization != "" {
		parts = append(parts, "organization_name = ?")
		args = append(args, filters.Organization)
	}
	if filters.CostCenterID != "" {
		parts = append(parts, "cost_center_id = ?")
		args = append(args, filters.CostCenterID)
	}
	return strings.Join(parts, " and "), args
}

func (c *Cache) Stats() (map[string]int, error) {
	tables := []string{"repos", "runs", "jobs", "billing_usage"}
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
	for _, table := range []string{"billing_usage", "jobs", "runs", "repos"} {
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

type WebScope struct {
	Filters QueryFilters
	Repos   []Repo
}

func (a *App) webHandler() (http.Handler, error) {
	return a.webHandlerWithScope(WebScope{})
}

func (a *App) webHandlerWithScope(scope WebScope) (http.Handler, error) {
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
		jobs, err := a.scopedJobs(scope, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = writeJSON(w, buildSummary(a.cache.Path(), jobs, scope.Filters, []string{"repo", "workflow-path", "job", "runner-image"}, a.now()))
	})
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		limit := 500
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				limit = parsed
			}
		}
		jobs, err := a.scopedJobs(scope, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = writeJSON(w, jobs)
	})
	return mux, nil
}

func (a *App) scopedJobs(scope WebScope, limit int) ([]JobRecord, error) {
	filters := scope.Filters
	if len(scope.Repos) == 0 {
		filters.Limit = limit
		return a.cache.ListJobs(filters)
	}
	filters.Limit = 0
	jobs, err := a.cache.ListJobs(filters)
	if err != nil {
		return nil, err
	}
	jobs = filterJobsByRepos(jobs, scope.Repos)
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
