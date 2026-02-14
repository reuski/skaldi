.PHONY: all build test lint vuln clean

BINARY_NAME=skaldi

all: test lint build

build:
	go build -o $(BINARY_NAME) ./cmd/skaldi

test:
	go test -v ./internal/...

lint:
	test -z $$(gofmt -l .)
	if command -v golangci-lint >/dev/null; then golangci-lint run; fi

vuln:
	if command -v govulncheck >/dev/null; then govulncheck ./...; fi

clean:
	go clean
	rm -f $(BINARY_NAME) dist/*
