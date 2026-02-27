set shell := ["bash", "-euo", "pipefail", "-c"]

binary := "skaldi"

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

release-build:
	mkdir -p dist
	for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
	  GOOS=${pair%/*} GOARCH=${pair#*/} \
	  go build -o dist/skaldi-${pair%/*}-${pair#*/} ./cmd/skaldi; \
	done

clean:
	go clean
	rm -f {{binary}} dist/*
