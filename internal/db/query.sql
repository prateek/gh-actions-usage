-- name: UpsertRepo :exec
INSERT INTO repos(full_name, id, account, owner, owner_kind, name, private, billing_owner, billing_owner_kind, billing_plan, raw_json, updated_at)
VALUES (
    sqlc.arg(full_name),
    sqlc.arg(id),
    sqlc.arg(account),
    sqlc.arg(owner),
    sqlc.arg(owner_kind),
    sqlc.arg(name),
    sqlc.arg(private),
    sqlc.arg(billing_owner),
    sqlc.arg(billing_owner_kind),
    sqlc.arg(billing_plan),
    sqlc.arg(raw_json),
    current_timestamp
)
ON CONFLICT(full_name) DO UPDATE SET
    id = excluded.id,
    account = excluded.account,
    owner = excluded.owner,
    owner_kind = excluded.owner_kind,
    name = excluded.name,
    private = excluded.private,
    billing_owner = excluded.billing_owner,
    billing_owner_kind = excluded.billing_owner_kind,
    billing_plan = excluded.billing_plan,
    raw_json = excluded.raw_json,
    updated_at = current_timestamp;

-- name: UpsertRun :exec
INSERT INTO runs(id, account, repo, repo_owner, repo_owner_kind, billing_owner, billing_owner_kind, billing_plan, workflow_id, workflow_name, workflow_path, run_number, run_attempt, event, branch, actor, status, conclusion, created_at, run_started_at, html_url, raw_json, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(account),
    sqlc.arg(repo),
    sqlc.arg(repo_owner),
    sqlc.arg(repo_owner_kind),
    sqlc.arg(billing_owner),
    sqlc.arg(billing_owner_kind),
    sqlc.arg(billing_plan),
    sqlc.arg(workflow_id),
    sqlc.arg(workflow_name),
    sqlc.arg(workflow_path),
    sqlc.arg(run_number),
    sqlc.arg(run_attempt),
    sqlc.arg(event),
    sqlc.arg(branch),
    sqlc.arg(actor),
    sqlc.arg(status),
    sqlc.arg(conclusion),
    sqlc.arg(created_at),
    sqlc.arg(run_started_at),
    sqlc.arg(html_url),
    sqlc.arg(raw_json),
    current_timestamp
)
ON CONFLICT(id) DO UPDATE SET
    account = excluded.account,
    repo = excluded.repo,
    repo_owner = excluded.repo_owner,
    repo_owner_kind = excluded.repo_owner_kind,
    billing_owner = excluded.billing_owner,
    billing_owner_kind = excluded.billing_owner_kind,
    billing_plan = excluded.billing_plan,
    workflow_id = excluded.workflow_id,
    workflow_name = excluded.workflow_name,
    workflow_path = excluded.workflow_path,
    run_number = excluded.run_number,
    run_attempt = excluded.run_attempt,
    event = excluded.event,
    branch = excluded.branch,
    actor = excluded.actor,
    status = excluded.status,
    conclusion = excluded.conclusion,
    created_at = excluded.created_at,
    run_started_at = excluded.run_started_at,
    html_url = excluded.html_url,
    raw_json = excluded.raw_json,
    updated_at = current_timestamp;

-- name: UpsertJob :exec
INSERT INTO jobs(id, run_id, account, repo, repo_owner, repo_owner_kind, billing_owner, billing_owner_kind, billing_plan, cost_class, workflow_name, workflow_path, name, status, conclusion, started_at, completed_at, duration_seconds, runner_name, runner_group, runner_type, runner_os, runner_arch, runner_image, labels_json, html_url, raw_json, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(run_id),
    sqlc.arg(account),
    sqlc.arg(repo),
    sqlc.arg(repo_owner),
    sqlc.arg(repo_owner_kind),
    sqlc.arg(billing_owner),
    sqlc.arg(billing_owner_kind),
    sqlc.arg(billing_plan),
    sqlc.arg(cost_class),
    sqlc.arg(workflow_name),
    sqlc.arg(workflow_path),
    sqlc.arg(name),
    sqlc.arg(status),
    sqlc.arg(conclusion),
    sqlc.arg(started_at),
    sqlc.arg(completed_at),
    sqlc.arg(duration_seconds),
    sqlc.arg(runner_name),
    sqlc.arg(runner_group),
    sqlc.arg(runner_type),
    sqlc.arg(runner_os),
    sqlc.arg(runner_arch),
    sqlc.arg(runner_image),
    sqlc.arg(labels_json),
    sqlc.arg(html_url),
    sqlc.arg(raw_json),
    current_timestamp
)
ON CONFLICT(id) DO UPDATE SET
    run_id = excluded.run_id,
    account = excluded.account,
    repo = excluded.repo,
    repo_owner = excluded.repo_owner,
    repo_owner_kind = excluded.repo_owner_kind,
    billing_owner = excluded.billing_owner,
    billing_owner_kind = excluded.billing_owner_kind,
    billing_plan = excluded.billing_plan,
    cost_class = excluded.cost_class,
    workflow_name = excluded.workflow_name,
    workflow_path = excluded.workflow_path,
    name = excluded.name,
    status = excluded.status,
    conclusion = excluded.conclusion,
    started_at = excluded.started_at,
    completed_at = excluded.completed_at,
    duration_seconds = excluded.duration_seconds,
    runner_name = excluded.runner_name,
    runner_group = excluded.runner_group,
    runner_type = excluded.runner_type,
    runner_os = excluded.runner_os,
    runner_arch = excluded.runner_arch,
    runner_image = excluded.runner_image,
    labels_json = excluded.labels_json,
    html_url = excluded.html_url,
    raw_json = excluded.raw_json,
    updated_at = current_timestamp;

-- name: UpsertBillingUsage :exec
INSERT INTO billing_usage("key", account, account_kind, date, year, month, day, product, sku, unit_type, model, organization_name, repository_name, cost_center_id, cost_class, quantity, gross_quantity, discount_quantity, net_quantity, price_per_unit, gross_amount, discount_amount, net_amount, raw_json, updated_at)
VALUES (
    sqlc.arg(key),
    sqlc.arg(account),
    sqlc.arg(account_kind),
    sqlc.arg(date),
    sqlc.arg(year),
    sqlc.arg(month),
    sqlc.arg(day),
    sqlc.arg(product),
    sqlc.arg(sku),
    sqlc.arg(unit_type),
    sqlc.arg(model),
    sqlc.arg(organization_name),
    sqlc.arg(repository_name),
    sqlc.arg(cost_center_id),
    sqlc.arg(cost_class),
    sqlc.arg(quantity),
    sqlc.arg(gross_quantity),
    sqlc.arg(discount_quantity),
    sqlc.arg(net_quantity),
    sqlc.arg(price_per_unit),
    sqlc.arg(gross_amount),
    sqlc.arg(discount_amount),
    sqlc.arg(net_amount),
    sqlc.arg(raw_json),
    current_timestamp
)
ON CONFLICT("key") DO UPDATE SET
    account = excluded.account,
    account_kind = excluded.account_kind,
    date = excluded.date,
    year = excluded.year,
    month = excluded.month,
    day = excluded.day,
    product = excluded.product,
    sku = excluded.sku,
    unit_type = excluded.unit_type,
    model = excluded.model,
    organization_name = excluded.organization_name,
    repository_name = excluded.repository_name,
    cost_center_id = excluded.cost_center_id,
    cost_class = excluded.cost_class,
    quantity = excluded.quantity,
    gross_quantity = excluded.gross_quantity,
    discount_quantity = excluded.discount_quantity,
    net_quantity = excluded.net_quantity,
    price_per_unit = excluded.price_per_unit,
    gross_amount = excluded.gross_amount,
    discount_amount = excluded.discount_amount,
    net_amount = excluded.net_amount,
    raw_json = excluded.raw_json,
    updated_at = current_timestamp;

-- name: ListRepos :many
SELECT
    CAST(coalesce(id, 0) AS integer) AS id,
    CAST(coalesce(account, '') AS text) AS account,
    CAST(coalesce(owner, '') AS text) AS owner,
    CAST(coalesce(owner_kind, '') AS text) AS owner_kind,
    CAST(coalesce(name, '') AS text) AS name,
    CAST(coalesce(full_name, '') AS text) AS full_name,
    CAST(coalesce(private, 0) AS integer) AS private,
    CAST(coalesce(billing_owner, '') AS text) AS billing_owner,
    CAST(coalesce(billing_owner_kind, '') AS text) AS billing_owner_kind,
    CAST(coalesce(billing_plan, '') AS text) AS billing_plan,
    CAST(coalesce(raw_json, '') AS text) AS raw_json
FROM repos
ORDER BY full_name;

-- name: ListRuns :many
SELECT
    CAST(coalesce(id, 0) AS integer) AS id,
    CAST(coalesce(account, '') AS text) AS account,
    CAST(coalesce(repo, '') AS text) AS repo,
    CAST(coalesce(repo_owner, '') AS text) AS repo_owner,
    CAST(coalesce(repo_owner_kind, '') AS text) AS repo_owner_kind,
    CAST(coalesce(billing_owner, '') AS text) AS billing_owner,
    CAST(coalesce(billing_owner_kind, '') AS text) AS billing_owner_kind,
    CAST(coalesce(billing_plan, '') AS text) AS billing_plan,
    CAST(coalesce(workflow_id, 0) AS integer) AS workflow_id,
    CAST(coalesce(workflow_name, '') AS text) AS workflow_name,
    CAST(coalesce(workflow_path, '') AS text) AS workflow_path,
    CAST(coalesce(run_number, 0) AS integer) AS run_number,
    CAST(coalesce(run_attempt, 0) AS integer) AS run_attempt,
    CAST(coalesce(event, '') AS text) AS event,
    CAST(coalesce(branch, '') AS text) AS branch,
    CAST(coalesce(actor, '') AS text) AS actor,
    CAST(coalesce(status, '') AS text) AS status,
    CAST(coalesce(conclusion, '') AS text) AS conclusion,
    CAST(coalesce(created_at, '') AS text) AS created_at,
    CAST(coalesce(run_started_at, '') AS text) AS run_started_at,
    CAST(coalesce(html_url, '') AS text) AS html_url,
    CAST(coalesce(raw_json, '') AS text) AS raw_json
FROM runs
WHERE (CAST(sqlc.arg(filter_repo) AS integer) = 0 OR repo = sqlc.arg(repo))
    AND (CAST(sqlc.arg(filter_since) AS integer) = 0 OR run_started_at >= sqlc.arg(since))
    AND (CAST(sqlc.arg(filter_until) AS integer) = 0 OR run_started_at <= sqlc.arg(until))
ORDER BY run_started_at DESC
LIMIT CAST(sqlc.arg(limit) AS integer);

-- name: ListJobs :many
SELECT
    CAST(coalesce(id, 0) AS integer) AS id,
    CAST(coalesce(run_id, 0) AS integer) AS run_id,
    CAST(coalesce(account, '') AS text) AS account,
    CAST(coalesce(repo, '') AS text) AS repo,
    CAST(coalesce(repo_owner, '') AS text) AS repo_owner,
    CAST(coalesce(repo_owner_kind, '') AS text) AS repo_owner_kind,
    CAST(coalesce(billing_owner, '') AS text) AS billing_owner,
    CAST(coalesce(billing_owner_kind, '') AS text) AS billing_owner_kind,
    CAST(coalesce(billing_plan, '') AS text) AS billing_plan,
    CAST(coalesce(cost_class, '') AS text) AS cost_class,
    CAST(coalesce(workflow_name, '') AS text) AS workflow_name,
    CAST(coalesce(workflow_path, '') AS text) AS workflow_path,
    CAST(coalesce(name, '') AS text) AS name,
    CAST(coalesce(status, '') AS text) AS status,
    CAST(coalesce(conclusion, '') AS text) AS conclusion,
    CAST(coalesce(started_at, '') AS text) AS started_at,
    CAST(coalesce(completed_at, '') AS text) AS completed_at,
    CAST(coalesce(duration_seconds, 0) AS real) AS duration_seconds,
    CAST(coalesce(runner_name, '') AS text) AS runner_name,
    CAST(coalesce(runner_group, '') AS text) AS runner_group,
    CAST(coalesce(runner_type, '') AS text) AS runner_type,
    CAST(coalesce(runner_os, '') AS text) AS runner_os,
    CAST(coalesce(runner_arch, '') AS text) AS runner_arch,
    CAST(coalesce(runner_image, '') AS text) AS runner_image,
    CAST(coalesce(labels_json, '[]') AS text) AS labels_json,
    CAST(coalesce(html_url, '') AS text) AS html_url,
    CAST(coalesce(raw_json, '') AS text) AS raw_json
FROM jobs
WHERE (CAST(sqlc.arg(filter_repo) AS integer) = 0 OR repo = sqlc.arg(repo))
    AND (CAST(sqlc.arg(filter_since) AS integer) = 0 OR started_at >= sqlc.arg(since))
    AND (CAST(sqlc.arg(filter_until) AS integer) = 0 OR started_at <= sqlc.arg(until))
ORDER BY duration_seconds DESC, started_at DESC
LIMIT CAST(sqlc.arg(limit) AS integer);

-- name: ListBillingUsage :many
SELECT
    CAST(coalesce("key", '') AS text) AS "key",
    CAST(coalesce(account, '') AS text) AS account,
    CAST(coalesce(account_kind, '') AS text) AS account_kind,
    CAST(coalesce(date, '') AS text) AS date,
    CAST(coalesce(year, 0) AS integer) AS year,
    CAST(coalesce(month, 0) AS integer) AS month,
    CAST(coalesce(day, 0) AS integer) AS day,
    CAST(coalesce(product, '') AS text) AS product,
    CAST(coalesce(sku, '') AS text) AS sku,
    CAST(coalesce(unit_type, '') AS text) AS unit_type,
    CAST(coalesce(model, '') AS text) AS model,
    CAST(coalesce(organization_name, '') AS text) AS organization_name,
    CAST(coalesce(repository_name, '') AS text) AS repository_name,
    CAST(coalesce(cost_center_id, '') AS text) AS cost_center_id,
    CAST(coalesce(cost_class, '') AS text) AS cost_class,
    CAST(coalesce(quantity, 0) AS real) AS quantity,
    CAST(coalesce(gross_quantity, 0) AS real) AS gross_quantity,
    CAST(coalesce(discount_quantity, 0) AS real) AS discount_quantity,
    CAST(coalesce(net_quantity, 0) AS real) AS net_quantity,
    CAST(coalesce(price_per_unit, 0) AS real) AS price_per_unit,
    CAST(coalesce(gross_amount, 0) AS real) AS gross_amount,
    CAST(coalesce(discount_amount, 0) AS real) AS discount_amount,
    CAST(coalesce(net_amount, 0) AS real) AS net_amount,
    CAST(coalesce(raw_json, '') AS text) AS raw_json
FROM billing_usage
WHERE (CAST(sqlc.arg(filter_account) AS integer) = 0 OR account = sqlc.arg(account))
    AND (CAST(sqlc.arg(filter_repo) AS integer) = 0 OR repository_name = sqlc.arg(repo))
    AND (CAST(sqlc.arg(filter_since) AS integer) = 0 OR date >= sqlc.arg(since))
    AND (CAST(sqlc.arg(filter_until) AS integer) = 0 OR date <= sqlc.arg(until))
    AND (CAST(sqlc.arg(filter_year) AS integer) = 0 OR year = sqlc.arg(year))
    AND (CAST(sqlc.arg(filter_month) AS integer) = 0 OR month = sqlc.arg(month))
    AND (CAST(sqlc.arg(filter_day) AS integer) = 0 OR day = sqlc.arg(day))
    AND (CAST(sqlc.arg(filter_product) AS integer) = 0 OR product = sqlc.arg(product))
    AND (CAST(sqlc.arg(filter_sku) AS integer) = 0 OR sku = sqlc.arg(sku))
    AND (CAST(sqlc.arg(filter_organization) AS integer) = 0 OR organization_name = sqlc.arg(organization))
    AND (CAST(sqlc.arg(filter_cost_center_id) AS integer) = 0 OR cost_center_id = sqlc.arg(cost_center_id))
ORDER BY net_amount DESC, date DESC
LIMIT CAST(sqlc.arg(limit) AS integer);

-- name: Stats :one
SELECT
    (SELECT count(*) FROM repos) AS repos,
    (SELECT count(*) FROM runs) AS runs,
    (SELECT count(*) FROM jobs) AS jobs,
    (SELECT count(*) FROM billing_usage) AS billing_usage;

-- name: ClearBillingUsage :exec
DELETE FROM billing_usage;

-- name: ClearJobs :exec
DELETE FROM jobs;

-- name: ClearRuns :exec
DELETE FROM runs;

-- name: ClearRepos :exec
DELETE FROM repos;
