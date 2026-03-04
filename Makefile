GO       := go
GOTAGS   := fts5
GOFLAGS  := -tags=$(GOTAGS)

XGORELEASER_IMAGE := oryd/xgoreleaser:1.26.0-2.14.1

.PHONY: build build-local test e2e lint vet tidy clean format plugin-dev

build:
	docker run --platform linux/amd64 --mount type=bind,source="$$(pwd)",target=/project \
		$(XGORELEASER_IMAGE) --snapshot --clean

build-local:
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -o bin/lumen .

test:
	CGO_ENABLED=1 $(GO) test $(GOFLAGS) ./...

e2e:
	CGO_ENABLED=1 $(GO) test -tags=$(GOTAGS),e2e -timeout=20m -v -count=1 ./...

lint:
	golangci-lint run

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin/ dist/

format:
	goimports -w .
	npx --yes prettier --write "**/*.{json,md,mdx,yaml,yml}"
	npx --yes doctoc --github README.md

plugin-dev: build-local
	@echo "Run: claude --plugin-dir ."

vhs:
	export CLAUDE_PLUGIN_ROOT=$(pwd) && cd testdata/fixtures/go && vhs ../../../docs/demo/demo.tape