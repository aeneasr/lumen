BINARY := agent-index
GO     := go

.PHONY: build test e2e lint vet tidy clean

build:
	CGO_ENABLED=1 $(GO) build -o $(BINARY) .

test:
	CGO_ENABLED=1 $(GO) test ./...

e2e:
	CGO_ENABLED=1 $(GO) test -tags=e2e -timeout=5m -v -count=1 ./...

lint:
	golangci-lint run

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)
