#!/usr/bin/env bash
# Sync README.md to docs/content/_index.md to avoid duplication
# Usage: ./scripts/sync-readme-to-docs.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README="$REPO_ROOT/README.md"
DOCS_OVERVIEW="$REPO_ROOT/docs/content/docs/overview.md"

if [[ ! -f "$README" ]]; then
  echo "Error: README.md not found at $README"
  exit 1
fi

# Extract content from README (skip the first H1 title line)
README_CONTENT=$(tail -n +3 "$README")

# Create overview.md with Hugo frontmatter
cat > "$DOCS_OVERVIEW" <<'FRONTMATTER'
---
title: Overview & Philosophy
weight: 0
---

FRONTMATTER

# Process README content and convert GitHub-style callouts to Hugo callouts
# Use awk for proper multi-line callout handling
echo "$README_CONTENT" | awk '
BEGIN { in_callout = 0; callout_type = ""; callout_content = "" }
/^> \[!WARNING\]/ { in_callout = 1; callout_type = "warning"; next }
/^> \[!NOTE\]/ { in_callout = 1; callout_type = "info"; next }
/^> \[!TIP\]/ { in_callout = 1; callout_type = "info"; next }
/^> / {
  if (in_callout) {
    line = substr($0, 3)  # Remove "> " prefix
    if (callout_content != "") callout_content = callout_content "\n"
    callout_content = callout_content line
    next
  }
}
{
  if (in_callout) {
    print "{{< callout type=\"" callout_type "\" >}}"
    print callout_content
    print "{{< /callout >}}"
    print ""
    in_callout = 0
    callout_type = ""
    callout_content = ""
  }
  print
}
END {
  if (in_callout) {
    print "{{< callout type=\"" callout_type "\" >}}"
    print callout_content
    print "{{< /callout >}}"
  }
}
' | sed \
  -e 's|docs/gmail\.md|configurations/gmail|g' \
  -e 's|docs/proton-bridge\.md|configurations/proton-bridge|g' \
  -e 's|docs/android\.md|configurations/android|g' \
  -e 's|docs/configuration\.md|configuration|g' \
  -e 's|docs/keybindings\.md|keybindings|g' \
  -e 's|docs/screener\.md|screener|g' \
  -e 's|docs/sending\.md|sending|g' \
  -e 's|docs/reading\.md|reading|g' \
  -e 's|images/|/images/|g' \
  >> "$DOCS_OVERVIEW"

echo "✅ Synced README.md → docs/content/docs/overview.md"
echo "   Next: Run 'make docs-build' to regenerate the site"
