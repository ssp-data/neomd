# GitHub repository
REPO := ssp-data/neomd
INSTALL_PATH := ~/.local/bin/neomd

.DEFAULT_GOAL := run-headless

## run-headless: start neomd daemon in background
run-headless:
	nohup neomd --headless >> ~/.local/share/neomd/daemon.log 2>&1 &

## run-syncthing: start syncthing in background (access UI via SSH tunnel from laptop: ssh -L 8385:localhost:8384 ti)
run-syncthing:
	nohup syncthing >> ~/syncthing.log 2>&1 &

## restart: stop and restart daemon
restart: stop run-headless

## stop: stop daemon
stop:
	pkill neomd || true
	sleep 1

## status: check if daemon is running
status:
	ps aux | grep neomd | grep -v grep || echo "neomd is not running"

## log: follow daemon logs
log:
	tail -f ~/.local/share/neomd/daemon.log

## version: show installed version
version:
	neomd --version

## reinstall: download latest GitHub release and restart daemon (requires v0.6.1+)
reinstall: stop
	@echo "Fetching latest release from GitHub..."
	@latest=$$(curl -s https://api.github.com/repos/$(REPO)/releases/latest | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'); \
	version=$${latest#v}; \
	echo "Latest version: $$latest"; \
	echo "Downloading neomd_$${version}_freebsd_amd64.tar.gz..."; \
	curl -L -o /tmp/neomd.tar.gz "https://github.com/$(REPO)/releases/download/$$latest/neomd_$${version}_freebsd_amd64.tar.gz"; \
	echo "Extracting..."; \
	tar -xzf /tmp/neomd.tar.gz -C /tmp; \
	echo "Installing to $(INSTALL_PATH)..."; \
	mkdir -p ~/.local/bin; \
	mv /tmp/neomd $(INSTALL_PATH); \
	chmod +x $(INSTALL_PATH); \
	rm -f /tmp/neomd.tar.gz; \
	echo "Done! Installed $$latest"
	@$(MAKE) run-headless
	@sleep 1
	@$(MAKE) status
