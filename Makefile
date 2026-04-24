BIN := gh-actions-usage
PREFIX ?= $(HOME)/.local
REMOTE ?= origin
SHOWBOAT_DOC ?= docs/showboat-demo.md
COVERAGE_MIN ?= 95.0

.PHONY: test
test: test-go test-e2e

.PHONY: test-go
test-go:
	go test ./...

.PHONY: test-unit
test-unit: test-go

.PHONY: test-integration
test-integration: test-go

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
	gofmt -w *.go

.PHONY: coverage
coverage:
	@tmp="$$(mktemp)"; \
	trap 'rm -f "$$tmp"' EXIT; \
	go test ./... -coverprofile="$$tmp"; \
	go tool cover -func="$$tmp"; \
	total="$$(go tool cover -func="$$tmp" | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}')"; \
	awk -v total="$$total" -v min="$(COVERAGE_MIN)" 'BEGIN { if (total + 0 < min + 0) { printf("coverage %.1f%% below %.1f%%\n", total, min); exit 1 } printf("coverage %.1f%% meets %.1f%%\n", total, min) }'

.PHONY: check
check: fmt test coverage build
	git diff --check

.PHONY: docs-check
docs-check: docs-showboat-check

.PHONY: docs-update
docs-update: docs-showboat-update

.PHONY: docs-showboat-check
docs-showboat-check:
	uvx showboat verify $(SHOWBOAT_DOC)

.PHONY: docs-showboat-update
docs-showboat-update:
	tmp="$$(mktemp "$(SHOWBOAT_DOC).XXXXXX")"; \
	uvx showboat verify $(SHOWBOAT_DOC) --output "$$tmp" || true; \
	if [ -s "$$tmp" ]; then mv "$$tmp" $(SHOWBOAT_DOC); else rm -f "$$tmp"; fi; \
	uvx showboat verify $(SHOWBOAT_DOC)

.PHONY: release-preflight
release-preflight:
	@test -n "$(VERSION)" || (echo "VERSION=vX.Y.Z is required" >&2; exit 2)
	@case "$(VERSION)" in v*) ;; *) echo "VERSION must start with v" >&2; exit 2;; esac
	@git diff --quiet || (echo "working tree has unstaged changes" >&2; git status --short; exit 1)
	@git diff --cached --quiet || (echo "working tree has staged changes" >&2; git status --short; exit 1)
	git fetch --tags $(REMOTE)
	@! git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null || (echo "tag $(VERSION) already exists" >&2; exit 1)
	gh auth status >/dev/null
	gh workflow view release.yml >/dev/null

.PHONY: release
release: check release-preflight
	git tag -a "$(VERSION)" -m "$(VERSION)"
	git push "$(REMOTE)" "$(VERSION)"
	@echo "pushed $(VERSION); GitHub Actions release workflow will publish the extension artifacts"

.PHONY: release-status
release-status:
	@test -n "$(VERSION)" || (echo "VERSION=vX.Y.Z is required" >&2; exit 2)
	gh run list --workflow release.yml --limit 5
	@gh release view "$(VERSION)" || echo "release $(VERSION) is not visible yet; check the workflow run above"
