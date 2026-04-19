BINARY  := neomd
CMD     := ./cmd/neomd
INSTALL := $(HOME)/.local/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build run install daemon clean test test-integration send-test vet fmt tidy release docs help check-go demo demo-reset demo-hp demo-hp-reset benchmark


.DEFAULT_GOAL := install

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
	CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -o neomd-freebsd ./cmd/neomd   # Build static binary for FreeBSD
## run: build and run
run: build
	./$(BINARY) $(ARGS)

## install: install to ~/.local/bin
install: build
	install -Dm755 $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"

## daemon: run in headless daemon mode
daemon: build
	./$(BINARY) --headless

## demo: run neomd with demo account (~/.config/neomd-demo/config.toml)
demo: build
	./$(BINARY) -config $(HOME)/.config/neomd-demo/config.toml

## demo-reset: reset demo account to first-run state (welcome screen + empty screener lists)
demo-reset:
	./scripts/reset-demo.sh $(HOME)/.config/neomd-demo

## demo-hp: run neomd with Hostpoint demo account (fast)
demo-hp: build
	./$(BINARY) -config $(HOME)/.config/neomd-demo-hostpoint/config.toml

## demo-hp-reset: reset Hostpoint demo to first-run state
demo-hp-reset:
	./scripts/reset-demo.sh $(HOME)/.config/neomd-demo-hostpoint

## benchmark: benchmark IMAP latency for Hostpoint and Gmail
benchmark:
	@echo "=== Hostpoint ==="
	@IMAP_HOST=imap.mail.hostpoint.ch IMAP_USER=simu@sspaeti.com IMAP_PASS=$$IMAP_PASS_SIMU ./scripts/imap-benchmark.sh
	@echo ""
	@echo "=== Gmail ==="
	@IMAP_HOST=imap.gmail.com IMAP_USER=neomd.demo@gmail.com IMAP_PASS=$$IMAP_APPPASS_GMAIL_NEOMD ./scripts/imap-benchmark.sh

## test: run all unit tests (fast, no network)
test:
	go test ./...

## test-integration: run integration tests against real IMAP/SMTP (sends emails to demo account)
test-integration:
	NEOMD_TEST_IMAP_HOST=imap.mail.hostpoint.ch \
	NEOMD_TEST_SMTP_HOST=asmtp.mail.hostpoint.ch \
	NEOMD_TEST_USER=neomd.demo@ssp.sh \
	NEOMD_TEST_PASS=$$IMAP_PASS_NEOMD_DEMO \
	NEOMD_TEST_FROM="Neomd Demo <neomd.demo@ssp.sh>" \
	go test ./internal/ -run TestIntegration -v -count=1 -timeout 120s

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
release: docs docs-build
	@test -n "$(VERSION)" || (echo "Usage: make release VERSION=v0.1.0" && exit 1)
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
	@echo "Tagged $(VERSION) — GitHub Actions will build and publish the release."

## docs: regenerate keybindings section in README.md from internal/ui/keys.go
docs:
	go run ./cmd/docs
	@./scripts/sync-readme-to-docs.sh

## docs-sync: sync README.md to docs/content/overview.md
docs-sync:
	./scripts/sync-readme-to-docs.sh

## docs-serve: serve Hugo docs site locally at http://localhost:1313
docs-serve:
	$(MAKE) -C docs serve

## docs-build: build Hugo docs site to docs/public/
docs-build:
	$(MAKE) -C docs build

## docs-clean: remove generated Hugo files
docs-clean:
	$(MAKE) -C docs clean

sync-headless:
	scp neomd-freebsd ti:~/neomd

## help: print this list
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
