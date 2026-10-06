BINARY  := neomd
CMD     := ./cmd/neomd
INSTALL := $(HOME)/.local/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
# Headless server host. Default "ti" (LAN, via ~/.ssh/config); away from home:
#   make sync-headless TI_HOST=ti.sspaeti.duckdns.org
TI_HOST ?= ti

.PHONY: build run install daemon clean test test-integration send-test vet fmt fmt-check tidy release docs help check-go demo-hp demo-hp-reset benchmark ooo


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
install: docs build
	install -Dm755 $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed to $(INSTALL)/$(BINARY)"

## daemon: run in headless daemon mode
daemon: build
	./$(BINARY) --headless

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
	go test ./internal/ -run TestIntegration -v -count=1 -timeout 300s

## send-test: send a test email to sspaeti@hey.com (override: make send-test TO=other@example.com)
send-test:
	go run ./cmd/sendtest $(TO)

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## fmt-check: report unformatted files (nonzero exit if any) — for CI / pre-commit
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files (run 'make fmt'):"; echo "$$unformatted"; exit 1; \
	fi

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
	@PREV=$$(git describe --tags --abbrev=0); \
	if git cat-file -e "$$PREV:RELEASE_NOTES.md" 2>/dev/null && git diff --quiet "$$PREV" -- RELEASE_NOTES.md; then \
	  echo "RELEASE_NOTES.md is unchanged since $$PREV — run: make release-notes VERSION=$(VERSION)"; exit 1; fi
	@git diff --quiet HEAD -- RELEASE_NOTES.md || (echo "RELEASE_NOTES.md has uncommitted changes — review and commit them first" && exit 1)
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
	@echo "Tagged $(VERSION) — GitHub Actions will build and publish the release."

## release-notes: draft RELEASE_NOTES.md for VERSION with Claude Code from CHANGELOG.md + commits since the last tag (PREV=vX.Y.Z to override); review, then commit it
release-notes:
	@test -n "$(VERSION)" || (echo "Usage: make release-notes VERSION=v0.1.0 [PREV=v0.0.9]" && exit 1)
	@command -v claude >/dev/null || (echo "claude CLI not found — install Claude Code or write RELEASE_NOTES.md by hand" && exit 1)
	@PREV="$(PREV)"; test -n "$$PREV" || PREV=$$(git describe --tags --abbrev=0); \
	echo "Drafting RELEASE_NOTES.md for $(VERSION) (changes since $$PREV)…"; \
	sed -e "s/{{VERSION}}/$(VERSION)/g" -e "s/{{PREV}}/$$PREV/g" scripts/release-notes-prompt.md \
	  | env -u ANTHROPIC_API_KEY claude -p --allowedTools "Read,Write,Bash(git log:*),Bash(git diff:*),Bash(git describe:*),Bash(head:*)"
	@# env -u ANTHROPIC_API_KEY: use the Claude Code login, not a stray API key from the shell
	@echo; echo "Review RELEASE_NOTES.md, then: git add RELEASE_NOTES.md && git commit -m 'release notes $(VERSION)'"

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

## ooo: sync out-of-office config (~/.config/neomd/ooo.toml) to the ti server (override host: TI_HOST=...) — daemon hot-reloads it, no restart needed
ooo:
	@test -f ~/.config/neomd/ooo.toml || { echo "ERROR: ~/.config/neomd/ooo.toml not found — create it first (enabled/until/subject/body, see docs headless page)"; exit 1; }
	scp ~/.config/neomd/ooo.toml $(TI_HOST):~/.config/neomd/ooo.toml
	@echo ""
	@echo "OOO config synced — daemon applies it on the next sync cycle (bg_sync_interval min at most). Current state on server:"
	@ssh $(TI_HOST) "grep -E '^(enabled|from|until|subject)' ~/.config/neomd/ooo.toml || true"

## sync-headless: deploy FreeBSD binary to the ti server (override host: TI_HOST=...) and restart daemon
sync-headless: build
	@echo "Stopping daemon (FreeBSD refuses to overwrite a running binary)..."
	ssh $(TI_HOST) "pkill neomd || true; sleep 2"
	@echo "Copying binary and Makefile to ti..."
	scp neomd-freebsd $(TI_HOST):~/.local/bin/neomd
	scp scripts/headless-server/Makefile $(TI_HOST):~/Makefile
	@echo "Starting daemon (sourcing ~/.profile so env vars like the IMAP password are loaded)..."
	ssh $(TI_HOST) ". ~/.profile; mkdir -p ~/.local/share/neomd; make run-headless"
	@echo "Waiting for daemon to start..."
	@sleep 2
	@echo "Checking status (first screening cycle must finish before the verdict is meaningful)..."
	@sleep 8
	@ssh $(TI_HOST) "make status"

## syncthing-tunnel: start syncthing on ti (if not running) and open SSH tunnel → http://localhost:8385
syncthing-tunnel:
	@echo "Starting syncthing on ti..."
	ssh ti "pgrep syncthing || nohup syncthing >> ~/syncthing.log 2>&1 &"
	@echo "Opening SSH tunnel — access Syncthing UI at http://localhost:8385"
	ssh -L 8385:localhost:8384 ti

## help: print this list
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
