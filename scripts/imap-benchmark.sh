#!/bin/bash
# Benchmark IMAP server latency (LOGIN + SELECT INBOX + UID SEARCH ALL).
# Usage: IMAP_HOST=imap.example.com IMAP_USER=me@example.com IMAP_PASS=secret ./scripts/imap-benchmark.sh
#
# Or with env vars from your shell:
#   IMAP_HOST=imap.mail.hostpoint.ch IMAP_USER=simu@sspaeti.com IMAP_PASS=$IMAP_PASS_SIMU ./scripts/imap-benchmark.sh
#   IMAP_HOST=imap.gmail.com IMAP_USER=demo@gmail.com IMAP_PASS=$GMAIL_APP_PASS ./scripts/imap-benchmark.sh
#
# OAuth2 support - reads token from neomd config:
#   CONFIG=~/.config/neomd/config.toml ./scripts/imap-benchmark.sh
#   CONFIG=~/.config/neomd-demo/config.toml ./scripts/imap-benchmark.sh

set -e

HOST="${IMAP_HOST:?Set IMAP_HOST (e.g. imap.gmail.com)}"
USER="${IMAP_USER:?Set IMAP_USER (e.g. me@example.com)}"
PASS="${IMAP_PASS:-}"
PORT="${IMAP_PORT:-993}"
CONFIG="${CONFIG:-}"

python3 -c "
import time, ssl, socket, json, base64, os, sys
from datetime import datetime, timezone

host, user, pw = '$HOST', '$USER', '$PASS'
port = $PORT
config_path = '$CONFIG'

def sanitize_account_name(name):
    '''Sanitize account name for token filename (same as Go)'''
    return ''.join('_' if c in '/\\\\:' else c for c in name)

def get_token_from_config(config_path, user):
    '''Read token from neomd config and token file'''
    import tomllib  # Python 3.11+

    with open(config_path, 'rb') as f:
        cfg = tomllib.load(f)

    # Find account matching the user
    accounts = cfg.get('accounts', [])
    if not accounts and cfg.get('account'):
        accounts = [cfg['account']]

    account = None
    for acc in accounts:
        if acc.get('user') == user:
            account = acc
            break

    if not account:
        print(f'Error: No account found for user {user} in {config_path}')
        sys.exit(1)

    # Check if OAuth2
    auth_type = account.get('auth_type', '').lower()
    if auth_type != 'oauth2':
        return None  # Not OAuth2, use password

    account_name = account.get('name', 'Personal')

    # Compute token file path (same logic as Go)
    # Go uses: ~/.config/{cacheDirName}/tokens/{safe_name}.json
    config_dir = os.path.dirname(config_path)  # e.g., ~/.config/neomd
    cache_dir_name = os.path.basename(config_dir)  # e.g., neomd
    safe_name = sanitize_account_name(account_name)

    # Get the parent of config_dir (the .config directory)
    config_parent = os.path.dirname(config_dir)  # e.g., ~/.config
    token_path = os.path.join(config_parent, cache_dir_name, 'tokens', f'{safe_name}.json')

    # Try alternate location (if running from different config)
    if not os.path.exists(token_path):
        home = os.path.expanduser('~')
        token_path = os.path.join(home, '.config', cache_dir_name, 'tokens', f'{safe_name}.json')

    if not os.path.exists(token_path):
        print(f'Error: Token file not found at {token_path}')
        print('Run neomd first to authenticate via OAuth2')
        sys.exit(1)

    with open(token_path) as f:
        token_data = json.load(f)

    # Check expiry
    expiry_str = token_data.get('expiry', '')
    if expiry_str:
        try:
            # Parse ISO format with or without Z
            expiry_str = expiry_str.replace('Z', '+00:00')
            expiry = datetime.fromisoformat(expiry_str)
            now = datetime.now(timezone.utc)
            if expiry < now:
                print(f'Error: Token expired at {expiry}')
                print('Run neomd to refresh the OAuth2 token')
                sys.exit(1)
        except ValueError as e:
            print(f'Warning: Could not parse token expiry: {e}')

    return token_data.get('access_token')

# Load token if config provided
token = None
if config_path:
    try:
        token = get_token_from_config(config_path, user)
        if token:
            print(f'Using OAuth2 token from config: {config_path}')
    except ImportError as e:
        print(f'Error: {e}')
        print('Note: OAuth2 support requires Python 3.11+ for tomllib')
        sys.exit(1)
    except Exception as e:
        print(f'Error reading config: {e}')
        sys.exit(1)

if not token and not pw:
    print('Error: Either IMAP_PASS or CONFIG (for OAuth2) must be provided')
    sys.exit(1)

print(f'Benchmarking {host}:{port} as {user}...')
print()

ctx = ssl.create_default_context()

# TLS connect
start = time.time()
s = ctx.wrap_socket(socket.socket(), server_hostname=host)
s.settimeout(10)
s.connect((host, port))
greeting = s.recv(4096)
tls_ms = (time.time() - start) * 1000

# Authenticate (LOGIN or XOAUTH2)
start = time.time()
if token:
    # XOAUTH2 SASL: base64(user={user}\x01auth=Bearer {token}\x01\x01)
    auth_str = f'user={user}\x01auth=Bearer {token}\x01\x01'
    auth_b64 = base64.b64encode(auth_str.encode()).decode()
    s.send(f'a1 AUTHENTICATE XOAUTH2 {auth_b64}\r\n'.encode())
else:
    s.send(f'a1 LOGIN {user} {pw}\r\n'.encode())

resp = b''
while b'a1 ' not in resp:
    resp += s.recv(4096)

# Check for authentication failure
resp_str = resp.decode(errors='replace')
if 'a1 NO' in resp_str or 'a1 BAD' in resp_str:
    print(f'Authentication failed: {resp_str.strip()}')
    sys.exit(1)

login_ms = (time.time() - start) * 1000

# SELECT INBOX
start = time.time()
s.send(b'a2 SELECT INBOX\r\n')
resp = b''
while b'a2 ' not in resp:
    resp += s.recv(4096)
select_ms = (time.time() - start) * 1000

# UID SEARCH ALL
start = time.time()
s.send(b'a3 UID SEARCH ALL\r\n')
resp = b''
while b'a3 ' not in resp:
    resp += s.recv(4096)
search_ms = (time.time() - start) * 1000

# Parse UIDs from SEARCH response and FETCH last 10 headers
uids = []
for line in resp.decode(errors='replace').split('\r\n'):
    if '* SEARCH' in line:
        uids = [u for u in line.split()[2:] if u.isdigit()]
        break

fetch_ms = 0
if uids:
    last_uids = uids[-min(10, len(uids)):]
    uid_range = ','.join(last_uids)
    start = time.time()
    s.send(f'a4 UID FETCH {uid_range} (UID FLAGS ENVELOPE RFC822.SIZE BODYSTRUCTURE)\r\n'.encode())
    resp = b''
    while b'a4 OK' not in resp:
        resp += s.recv(8192)
    fetch_ms = (time.time() - start) * 1000

# MOVE one email (to itself = NOOP, just measures command latency)
move_ms = 0
if uids:
    start = time.time()
    s.send(b'a5 NOOP\r\n')
    resp = b''
    while b'a5 ' not in resp:
        resp += s.recv(4096)
    move_ms = (time.time() - start) * 1000

s.send(b'a9 LOGOUT\r\n')
s.close()

total = login_ms + select_ms + search_ms + fetch_ms
print(f'  TLS connect:  {tls_ms:6.0f}ms')
if token:
    print(f'  XOAUTH2:      {login_ms:6.0f}ms')
else:
    print(f'  LOGIN:        {login_ms:6.0f}ms')
print(f'  SELECT INBOX: {select_ms:6.0f}ms')
print(f'  UID SEARCH:   {search_ms:6.0f}ms')
n = min(10, len(uids))
print(f'  FETCH ({n:>2} hdr):{fetch_ms:6.0f}ms')
print(f'  NOOP:         {move_ms:6.0f}ms')
print(f'  ─────────────────────')
print(f'  Total:        {total:6.0f}ms')
print()
if total < 100:
    print('  Result: Excellent — neomd will feel instant')
elif total < 300:
    print('  Result: Good — neomd will feel responsive')
elif total < 1000:
    print('  Result: Slow — noticeable delay on folder switches')
else:
    print('  Result: Very slow — neomd is not recommended with this provider')
"
