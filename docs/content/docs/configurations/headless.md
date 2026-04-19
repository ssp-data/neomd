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

### What Gets Synced

**Screener list directory**: `~/.config/neomd/lists/` - you can also sync the entire `neomd` folder, if you don't have passwords stored in there, only ENVs:
- `screened_in.txt`
- `screened_out.txt`
- `feed.txt`
- `papertrail.txt`
- `spam.txt`

### Step-by-Step Setup

#### 1. Install Syncthing

**On Arch Linux / Server:**
```sh
sudo pacman -S syncthing
systemctl --user enable syncthing
systemctl --user start syncthing
```

**On other systems**: See [Syncthing installation docs](https://docs.syncthing.net/intro/getting-started.html)

> **Note**: Use `systemctl --user` instead of adding to window manager autostart scripts. This ensures Syncthing runs on login, works across different environments, and continues running independently of your desktop session.

#### 2. Access Web UI

Open http://localhost:8384 on each device

#### 3. Connect Devices

**On Device A (e.g., your laptop):**
1. Go to **Actions** → **Show ID** to get your Device ID
2. Copy the long alphanumeric Device ID

**On Device B (e.g., your server):**
1. Click **Add Remote Device**
2. Paste Device A's Device ID
3. Name it (e.g., "laptop")
4. Click **Save**

**Back on Device A:**
1. Accept the connection notification
2. Name Device B (e.g., "server")
3. Click **Save**

Repeat for all devices (laptop, server, Android).

#### 4. Create Shared Folder

**On one device (e.g., server):**
1. Click **Add Folder**
2. Set **Folder Label**: `neomd-lists`
3. Set **Folder ID**: `neomd-lists` (same on all devices)
4. Set **Folder Path**: `/home/user/.config/neomd/lists/` (or `~/.config/neomd/` if syncing entire folder)
5. Go to **Sharing** tab → check all other devices
6. Go to **File Versioning** tab:
   - Select **Simple File Versioning**
   - Keep Versions: `5`
7. Click **Save**

**On other devices:**
1. Accept the folder share notification
2. Verify/set the correct path for that device
3. Enable **File Versioning** (same as above)
4. Click **Save**

#### 5. Backup First (Important!)

Before syncing existing data, **backup your lists**:

```sh
cp -r ~/.config/neomd/lists ~/.config/neomd/lists.backup-$(date +%Y%m%d)
```

#### 6. Wait for Initial Sync

Watch the folder status in the web UI. It will show "Syncing" with progress, then "Up to Date" when complete.

### Server Setup (FreeBSD / No GUI)

If running neomd headless on a FreeBSD server without a desktop environment, use SSH port forwarding to access the Syncthing web UI:

#### 1. Start Syncthing on FreeBSD

```sh
# Enable and start as system service
sudo sysrc syncthing_enable="YES"
sudo sysrc syncthing_user="sspaeti"
sudo service syncthing start

# Or run as user service (no sudo)
syncthing &

# Or with nohup for persistent operation
nohup syncthing > ~/syncthing.log 2>&1 &
```

Check it's running:
```sh
ps aux | grep syncthing
```

#### 2. SSH Port Forwarding

From your **local machine** (laptop/desktop with browser), create an SSH tunnel:

```sh
ssh -L 8385:localhost:8384 your-server
```

This forwards `localhost:8385` on your local machine to `localhost:8384` on the server.

Now open in your **local browser**: http://localhost:8385

You'll see the server's Syncthing web UI!

#### 3. Get Server Device ID

In the web UI at http://localhost:8385:
1. Go to **Actions** → **Show ID**
2. Copy the Device ID

#### 4. Connect Your Devices

**On your local machine's Syncthing** (http://localhost:8384):
1. Click **Add Remote Device**
2. Paste the server's Device ID
3. Name it (e.g., "freebsd-server")
4. Click **Save**

**On the server's UI** (http://localhost:8385 via SSH tunnel):
1. Accept the connection notification
2. Name your local device (e.g., "laptop")
3. Click **Save**

#### 5. Share the Folder

**On your local machine** (http://localhost:8384):
1. Find your existing `neomd-lists` folder
2. Click **Edit**
3. Go to **Sharing** tab
4. Check the box next to your server device
5. Click **Save**

**On the server** (http://localhost:8385):
1. Accept the folder share notification
2. Set **Folder Path**: `/home/user/.config/neomd/lists/` (or `~/.config/neomd/` if syncing entire folder)
3. Go to **File Versioning** tab:
   - Select **Simple File Versioning**
   - Keep Versions: `5`
4. Click **Save**

#### 6. Handle Existing Files

Before syncing, **backup the server's existing lists**:

```sh
# On server
cp -r ~/.config/neomd/lists ~/.config/neomd/lists.backup-$(date +%Y%m%d)
```

Syncthing will merge files from both sides. To **start fresh from your local machine's data**:

```sh
# On server - remove existing files (after backup!)
rm -rf ~/.config/neomd/lists/*
```

#### 7. Close SSH Tunnel

Once setup is complete, you can close the SSH tunnel (Ctrl+C in the SSH session). Devices will continue syncing in the background.

For future configuration changes, create the SSH tunnel again when needed:
```sh
ssh -L 8385:localhost:8384 your-server
```

### Verify Sync is Working

```sh
# Check files exist
ls -la ~/.config/neomd/lists/

# Watch real-time sync in logs
journalctl --user -u syncthing -f
```

The daemon watches for file changes and reloads screener lists automatically when Syncthing updates them.

### Conflict Handling

- **File-level conflicts**: Syncthing creates `.sync-conflict-*` files if two devices modify the same file simultaneously
- **Email-level**: IMAP is the source of truth; no local email state to conflict
- **Screener lists**: Append-only operations are safe; duplicates are harmless (normalized automatically)

Check for conflicts periodically:
```sh
find ~/.config/neomd/lists -name "*.sync-conflict-*"
```

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
