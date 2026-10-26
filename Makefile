# Build the tau binary. Override the output with `make OUT=path`.
OUT ?= tau

.PHONY: build test vet

build:
	go build -o $(OUT) ./cmd/tau

test:
	go test -race ./...

vet:
	gofmt -l . | (! grep .)
	go vet ./...
