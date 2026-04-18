---
title: Headless Daemon Mode
weight: 4
---

Neomd can run in headless daemon mode to continuously screen emails in the background without launching the TUI. This is useful for running neomd on a server (like a NAS) that screens emails automatically, while you use the TUI on your laptop or mobile device.

## Overview

When running in headless mode, neomd:
- **Screens emails automatically** every `bg_sync_interval` minutes
- **Watches screener list files** and reloads when they change (via Syncthing)
- **Runs in the background** as a standard process
- **Logs to stdout** for monitoring

## Quick Start

```sh
# Run in foreground (for testing)
neomd --headless

# Run in background
neomd --headless &

# Run in background with logging
nohup neomd --headless > /var/log/neomd.log 2>&1 &

# Or redirect to a file
neomd --headless >> ~/.local/share/neomd/daemon.log 2>&1 &
```

## Multi-Device Setup with Syncthing

The headless daemon is designed to work with [Syncthing](https://syncthing.net/) to keep screener lists synchronized across multiple devices.

### Architecture

1. **NAS/Server**: Runs `neomd --headless` continuously, screening emails every 5 minutes
2. **Laptop**: Runs TUI with `bg_sync_interval = 0` (disabled), classifies senders manually
3. **Android/Mobile**: Runs TUI with `bg_sync_interval = 0` (disabled), classifies senders manually
4. **Syncthing**: Syncs screener list files across all devices

### Benefits

- **Automatic screening**: Emails are screened on the server even when your laptop/phone is offline
- **Mobile email apps work**: Your phone's native email app sees screened emails in the correct IMAP folders
- **No conflicts**: Only the daemon moves emails; TUI instances only classify senders
- **Instant sync**: Classification decisions propagate to all devices via Syncthing

## Configuration

### Server Config (Daemon)

On your NAS/server, set `bg_sync_interval` to enable periodic screening:

```toml
# ~/.config/neomd/config.toml (server)
[ui]
bg_sync_interval = 5  # Screen inbox every 5 minutes
```

### Laptop/Mobile Config (TUI)

On devices where you run the TUI, **disable background sync** to avoid duplicate moves:

```toml
# ~/.config/neomd/config.toml (laptop/mobile)
[ui]
bg_sync_interval = 0  # Disable background screening (daemon handles it)
```

## Syncthing Setup

### Files to Sync

Configure Syncthing to sync the following files across all your devices:

1. **Screener list directory**: `~/.config/neomd/lists/`
   - `screened_in.txt`
   - `screened_out.txt`
   - `feed.txt`
   - `papertrail.txt`
   - `spam.txt`

2. **Config file** (optional): `~/.config/neomd/config.toml`
   - Useful for keeping settings consistent across devices
   - Be careful with account-specific settings (passwords, paths)

### Syncthing Folder Setup

1. **Install Syncthing** on all devices (NAS, laptop, Android)
2. **Create a shared folder** named "neomd-lists"
3. **Set folder path** to `~/.config/neomd/lists/` on each device
4. **Connect devices** and wait for initial sync

Example Syncthing folder configuration:
- **Folder ID**: `neomd-lists`
- **Folder Path**: `/home/user/.config/neomd/lists/`
- **Folder Type**: Send & Receive
- **File Versioning**: Simple File Versioning (keep 5 versions)

### Conflict Handling

- **File-level conflicts**: Syncthing creates `.sync-conflict` files if two devices modify the same file simultaneously
- **Email-level**: IMAP is the source of truth; no local email state to conflict
- **Screener lists**: Append-only operations are safe; duplicates are harmless (normalized automatically)

The daemon watches for file changes and reloads screener lists automatically when Syncthing updates them.

## Systemd Service (Optional)

For servers with systemd, you can create a service unit for automatic startup and logging:

```ini
# /etc/systemd/user/neomd.service
[Unit]
Description=Neomd Headless Email Screener
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/neomd --headless
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

Enable and start the service:

```sh
# Install neomd to ~/.local/bin
make install

# Reload systemd
systemctl --user daemon-reload

# Enable auto-start on login
systemctl --user enable neomd

# Start now
systemctl --user start neomd

# Check status
systemctl --user status neomd

# View logs
journalctl --user -u neomd -f
```

## Monitoring

### View Logs

If running with `nohup` or redirected output:

```sh
tail -f /var/log/neomd.log
```

If running as systemd service:

```sh
journalctl --user -u neomd -f
```

### Log Format

The daemon logs structured output with timestamps:

```
time=2025-04-18T10:00:00Z level=INFO msg="neomd daemon starting" version=headless
time=2025-04-18T10:00:00Z level=INFO msg="screening interval configured" minutes=5
time=2025-04-18T10:00:00Z level=INFO msg="watching directory for changes" dir=/home/user/.config/neomd/lists
time=2025-04-18T10:00:00Z level=INFO msg="daemon running" interval=5m0s
time=2025-04-18T10:00:05Z level=INFO msg="running initial screening"
time=2025-04-18T10:00:05Z level=INFO msg="fetched inbox emails" count=42
time=2025-04-18T10:00:05Z level=INFO msg="emails to screen" count=12
time=2025-04-18T10:00:05Z level=INFO msg="screened email" index=1 total=12 from="newsletter@example.com" subject="Weekly Update" dst=Feed
...
time=2025-04-18T10:00:06Z level=INFO msg="screening complete" moved=12 total=12
```

### Graceful Shutdown

Send SIGTERM or SIGINT to stop the daemon:

```sh
# If running in foreground
Ctrl+C

# If running in background
kill <pid>

# With systemd
systemctl --user stop neomd
```

The daemon will finish the current screening operation before exiting.

## Troubleshooting

### Daemon exits immediately

Check that `bg_sync_interval` is set to a value > 0:

```sh
grep bg_sync_interval ~/.config/neomd/config.toml
```

### Screener lists not reloading

Check file watcher logs:

```sh
tail -f /var/log/neomd.log | grep "watching directory"
```

Verify Syncthing is running and syncing:

```sh
# Check Syncthing web UI (usually http://localhost:8384)
```

### Emails not being screened

1. Check daemon is running: `ps aux | grep neomd`
2. Check IMAP connection in logs
3. Verify screener list files exist and contain email addresses
4. Check folder configuration in config.toml

### Duplicate screening

If emails are being moved twice (once by daemon, once by TUI):

- Set `bg_sync_interval = 0` on TUI devices
- Only run one daemon instance per account

## Android Termux Example

>[!NOTE] 
> See Android Termux Setup at [Android Docs](android.md)

On Android, you can run the daemon in a Termux session:

```sh
# Install Termux:Boot from F-Droid to auto-start on device boot
pkg install termux-boot

# Create boot script
mkdir -p ~/.termux/boot
cat > ~/.termux/boot/neomd-daemon.sh <<'EOF'
#!/data/data/com.termux/files/usr/bin/bash
cd ~/neomd
nohup ./neomd --headless >> ~/neomd-daemon.log 2>&1 &
EOF
chmod +x ~/.termux/boot/neomd-daemon.sh

# Reboot device to auto-start daemon
```

## Notes

- The daemon only **reads** screener list files and **moves** emails via IMAP
- All sender classification (adding to lists) happens in the TUI
- File watching requires the screener list directory to exist
- The daemon uses the first configured account from `config.toml`
- IMAP connection is kept alive and automatically reconnects on failures
