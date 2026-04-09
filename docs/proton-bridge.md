# Configuring neomd with Proton Mail Bridge

Proton Mail Bridge allows you to use neomd with ProtonMail accounts by running a local IMAP/SMTP bridge.

## Installation

1. Install Proton Mail Bridge: https://proton.me/mail/bridge
2. Launch the bridge and configure your ProtonMail account
3. Note the IMAP and SMTP connection details (typically `127.0.0.1:1143` and `127.0.0.1:1025`)

## neomd Configuration

Add the following to your `~/.config/neomd/config.toml`:

```toml
[[accounts]]
  name = "ProtonMail"
  imap = "127.0.0.1:1143"
  smtp = "127.0.0.1:1025"
  user = "your-proton-email@proton.me"
  password = "bridge-password-here"  # Get this from Proton Bridge settings
  from = "Your Name <your-proton-email@proton.me>"
  starttls = false  # Proton Bridge uses TLS on non-standard ports
```

## Key Configuration Details

- **IMAP Port**: Proton Bridge defaults to `1143` with TLS
- **SMTP Port**: Proton Bridge defaults to `1025` with STARTTLS
  - If you need STARTTLS for SMTP, set `starttls = true`
- **Password**: Use the bridge-generated password (not your ProtonMail password)
- **TLS/STARTTLS**: neomd automatically detects the correct security mode based on:
  - Standard ports (993→TLS, 143→STARTTLS for IMAP; 465→TLS, 587→STARTTLS for SMTP)
  - Non-standard ports default to TLS unless `starttls = true` is set
  - Explicit `starttls = true` always forces STARTTLS

## Troubleshooting

### "refusing unencrypted connection to 127.0.0.1:1143"

This error occurred in older versions of neomd that didn't respect the `starttls` config or handle non-standard ports correctly. **This is now fixed** (v0.4.15+).

If you still see this error:
1. Ensure you're running the latest version: `neomd --version`
2. Check your config has the correct IMAP/SMTP addresses
3. Verify Proton Bridge is running: `ps aux | grep bridge`

### Connection Refused

- Make sure Proton Mail Bridge is running
- Verify the bridge ports in its settings (they may differ from the defaults)
- Check firewall settings allow localhost connections

## Custom Ports

If your Proton Bridge uses different ports, adjust the config accordingly:

```toml
imap = "127.0.0.1:YOUR_IMAP_PORT"
smtp = "127.0.0.1:YOUR_SMTP_PORT"
```

For non-standard ports, neomd defaults to TLS. If your bridge uses STARTTLS on a custom port, add:

```toml
starttls = true
```
