BIN := gh-actions-usage
PREFIX ?= $(HOME)/.local

.PHONY: test
test: test-unit test-integration test-e2e

.PHONY: test-unit
test-unit:
	go test ./... -run '^Test(CreatedQuery|DefaultCachePath.*|RepoUnmarshalCapturesOwnerAndRaw|RunnerMetadata|SummaryGroupsByWorkflowAndRunner)$$'

.PHONY: test-integration
test-integration:
	go test ./... -run '^Test(CacheUpsertsAreIdempotent|OpenCacheCreatesParentDirectoryPrivate|RunSummaryCommandReadsCache|ReportCommand.*|TopLevelIngestCommandIsNotPublic|DoctorIngestActionsRunsManualRefresh|ServeRefreshRequiresAPIClient|ImportCommandIsIdempotent|ExportCommandIncludesRepos|WebHandler.*)$$'

.PHONY: test-e2e
test-e2e:
	scripts/e2e-fixture.sh

.PHONY: build
build:
	mkdir -p bin
	go build -o bin/$(BIN) .

.PHONY: install-local
install-local: build
	mkdir -p $(PREFIX)/bin
	cp bin/$(BIN) $(PREFIX)/bin/$(BIN)

.PHONY: install-gh-local
install-gh-local:
	gh extension remove actions-usage 2>/dev/null || true
	gh extension install .

.PHONY: fmt
fmt:
	gofmt -w main.go main_test.go

.PHONY: check
check: fmt test build
	git diff --check
