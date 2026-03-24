BINARY  := neomd
CMD     := ./cmd/neomd
INSTALL := $(HOME)/.local/bin

.PHONY: build run install clean test vet fmt lint tidy

## build: compile the binary into ./neomd
build:
	go build -o $(BINARY) $(CMD)

## run: build and run (pass ARGS="--config /path/to/config.toml" to override)
run: build
	./$(BINARY) $(ARGS)

## install: install the binary to ~/.local/bin
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

## clean: remove the compiled binary
clean:
	rm -f $(BINARY)

## help: print this help
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
