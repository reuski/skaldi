set shell := ["bash", "-euo", "pipefail", "-c"]

binary := "skaldi"
legacy_darwin_go_toolchain := env_var_or_default("SKALDI_LEGACY_DARWIN_GOTOOLCHAIN", "go1.24.13")

default: all

all: lint test build

fmt:
	gofmt -w .

fmt-check:
	test -z "$(gofmt -l .)"

lint: fmt-check
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run
	go vet ./...

test:
	go test -v -race ./internal/...

build:
	go build -o {{binary}} ./cmd/skaldi

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

release-build: release-build-current release-build-legacy-darwin

release-build-current:
	mkdir -p dist
	for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
	  GOOS=${pair%/*} GOARCH=${pair#*/} \
	  go build -o dist/skaldi-${pair%/*}-${pair#*/} ./cmd/skaldi; \
	done

release-build-legacy-darwin:
	mkdir -p dist
	for pair in darwin/amd64 darwin/arm64; do \
	  GOOS=${pair%/*} GOARCH=${pair#*/} \
	  GOTOOLCHAIN={{legacy_darwin_go_toolchain}} go build -o dist/skaldi-${pair%/*}-${pair#*/}-macos11 ./cmd/skaldi; \
	done

clean:
	go clean
	rm -f {{binary}} dist/*
