BIN := gh-actions-usage
PREFIX ?= $(HOME)/.local

.PHONY: test
test:
	go test ./...

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
