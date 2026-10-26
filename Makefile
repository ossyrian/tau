# Build the tau binary. The output is ./tau because /usr/local/bin/tau,
# tau.bash and tau.fish all point there. Override with `make OUT=path`.
OUT ?= tau

.PHONY: build test vet

build:
	go build -o $(OUT) ./cmd/tau

test:
	go test -race ./...

vet:
	gofmt -l . | (! grep .)
	go vet ./...
