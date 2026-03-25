BINARY  := neomd
CMD     := ./cmd/neomd
INSTALL := $(HOME)/.local/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build run install clean test vet fmt tidy release docs help

## build: compile ./neomd (version from git tag)
build: docs
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

## run: build and run
run: build
	./$(BINARY) $(ARGS)

## install: install to ~/.local/bin
install: build
	install -Dm755 $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"

## test: run all tests
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## tidy: tidy go.mod and go.sum
tidy:
	go mod tidy

## clean: remove compiled binary
clean:
	rm -f $(BINARY)

## release: tag and push a new release (usage: make release VERSION=v0.1.0)
release: docs
	@test -n "$(VERSION)" || (echo "Usage: make release VERSION=v0.1.0" && exit 1)
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
	@echo "Tagged $(VERSION) — GitHub Actions will build and publish the release."

## docs: regenerate keybindings section in README.md from internal/ui/keys.go
docs:
	go run ./cmd/docs

## help: print this list
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
