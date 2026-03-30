BINARY  := neomd
CMD     := ./cmd/neomd
INSTALL := $(HOME)/.local/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build run install clean test send-test vet fmt tidy release docs help check-go

## check-go: verify Go is installed
check-go:
	@command -v go >/dev/null 2>&1 || { \
		echo ""; \
		echo "  Error: Go is not installed."; \
		echo ""; \
		echo "  Install Go 1.22+ from https://go.dev/doc/install"; \
		echo ""; \
		echo "  Quick install (Linux):"; \
		echo "    curl -LO https://go.dev/dl/go1.24.2.linux-amd64.tar.gz"; \
		echo "    sudo tar -C /usr/local -xzf go1.24.2.linux-amd64.tar.gz"; \
		echo '    echo "export PATH=$$PATH:/usr/local/go/bin" >> ~/.bashrc'; \
		echo "    source ~/.bashrc"; \
		echo ""; \
		exit 1; \
	}

## build: compile ./neomd (version from git tag)
build: check-go docs
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

## run: build and run
run: build
	./$(BINARY) $(ARGS)

## install: install to ~/.local/bin
install: build
	install -Dm755 $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"


initialized-welcome-screen:
	rm ~/.cache/neomd/welcome-shown

## test: run all tests
test:
	go test ./...

## send-test: send a test email to sspaeti@hey.com (override: make send-test TO=other@example.com)
send-test:
	go run ./cmd/sendtest $(TO)

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## tidy: tidy go.mod and go.sum
tidy:
	go mod tidy

## android: cross-compile for Android ARM64 (run in Termux)
android: check-go docs
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-android $(CMD)
	@echo ""
	@echo "  Built $(BINARY)-android (ARM64)"
	@echo ""
	@echo "  Transfer to your Android device:"
	@echo "    adb push $(BINARY)-android /sdcard/Download/"
	@echo ""
	@echo "  Then in Termux:"
	@echo "    cp /sdcard/Download/$(BINARY)-android ~/$(BINARY)"
	@echo "    chmod +x ~/$(BINARY)"
	@echo "    ~/$(BINARY)"
	@echo ""

## clean: remove compiled binary
clean:
	rm -f $(BINARY) $(BINARY)-android

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
