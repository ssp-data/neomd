.DEFAULT_GOAL := run-headless

## check-go: verify Go is installed
runn-headless:
	nohup neomd --headless >> ~/.local/share/neomd/daemon.log 2>&1 &

log:
	cat ~/.local/share/neomd/daemon.log

restart:
	pkill neomd || true
	sleep 1
	nohup neomd --headless >> ~/.local/share/neomd/daemon.log 2>&1 &

stop:
	pkill neomd || true

status:
	ps aux | grep neomd | grep -v grep || echo "neomd is not running"
