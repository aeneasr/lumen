BINARY   := agent-index
GO       := go
GOTAGS   := fts5
GOFLAGS  := -tags=$(GOTAGS)

.PHONY: build test e2e lint vet tidy clean format

build:
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -o $(BINARY) .

test:
	CGO_ENABLED=1 $(GO) test $(GOFLAGS) ./...

install:
	CGO_ENABLED=1 $(GO) install $(GOFLAGS) ./...

e2e:
	CGO_ENABLED=1 $(GO) test -tags=$(GOTAGS),e2e -timeout=5m -v -count=1 ./...

lint:
	golangci-lint run

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)

format:
	goimports -w .
	npx --yes prettier --write "**/*.{json,md,mdx,yaml,yml}"
