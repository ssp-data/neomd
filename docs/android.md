# Android (Termux)

Neomd runs on Android via [Termux](https://f-droid.org/en/packages/com.termux/). Build natively on the phone — cross-compiled binaries have DNS issues on Android.

## Install

```sh
pkg install golang git
git clone https://github.com/sspaeti/neomd
cd neomd
go build -o neomd ./cmd/neomd
./neomd
```

On first run, neomd creates `~/.config/neomd/config.toml`. Edit it with your IMAP/SMTP credentials:

```sh
nano ~/.config/neomd/config.toml
```

Then run `./neomd` again.

## Why build on-device?

Cross-compiling with `CGO_ENABLED=0 GOOS=android` produces a working binary, but Go's pure-Go DNS resolver can't resolve hostnames on Android (no `/etc/resolv.conf`). Building natively in Termux uses the system C resolver, so DNS works out of the box.

A `make android` target exists for cross-compilation if you want to try it:

```sh
# on your Linux/Mac machine
make android
# transfer neomd-android to phone via LocalSend, adb, etc.
```

## Notes

- **Screen width**: works best on a tablet or with a Bluetooth keyboard; phone screens (~40-50 columns) are fine for reading and triaging
- **Composing**: shells out to `$EDITOR` (defaults to `nvim`); install neovim in Termux with `pkg install neovim`
- **Attachments**: `xdg-open` doesn't exist on Android; install `termux-api` and use `termux-open` instead (not yet auto-detected by neomd)
