#!/bin/bash
#
# Fail if a tracked file contains something that should never be published:
# a real credential, a private network address, or a private key. This
# project is open source and the feature that motivated its login support
# arrived with a working username and password for somebody's device; that
# pair belongs in that device's own /etc/cue/cue.yaml and nowhere else.
#
# Every pattern below is deliberately written to match a *shape*, not a
# literal, so that this script does not itself become the place a secret is
# recorded.

set -euo pipefail

cd "$(dirname "$0")/.."

STATUS=0

function report
{
    echo "check-secrets: $1" >&2
    STATUS=1
}

# The files to scan: everything git tracks, minus vendored code, minus this
# script, minus lock files full of hashes.
mapfile -t FILES < <(git ls-files \
    | grep -v '^vendor/' \
    | grep -v '^web/package-lock.json$' \
    | grep -v '^scripts/check-secrets.bash$')

if [ "${#FILES[@]}" -eq 0 ]; then
    exit 0
fi

# A private address. Documentation must use example.com, example.net or a
# name; a literal RFC 1918 address in a tracked file is almost always somebody
# real network leaking into the repository. 127.0.0.1 and 0.0.0.0 are fine and
# are excluded by the pattern.
if grep -nEI '(^|[^0-9.])(10\.[0-9]{1,3}|192\.168|172\.(1[6-9]|2[0-9]|3[01]))\.[0-9]{1,3}\.[0-9]{1,3}' "${FILES[@]}"; then
    report "a private network address is in a tracked file; use example.com or a placeholder"
fi

# A password, secret or token given a value that is not obviously a
# placeholder. Empty strings, the placeholders below, and Go struct fields
# (which are matched by the type after the name) do not count.
if grep -nEI '(password|passwd|secret|token|apikey|api_key)["'"'"']?\s*[:=]\s*["'"'"'][^"'"'"']+["'"'"']' "${FILES[@]}" \
    | grep -vEi '["'"'"'](|changeme|placeholder|redacted|example|secret|password|hunter2|\.\.\.|\$\{[^}]+\}|<[^>]+>)["'"'"']' \
    | grep -vE '\.go:[0-9]+:\s*//' \
    | grep -vE '_test\.go:' ; then
    report "what looks like a real credential is in a tracked file"
fi

# A private key of any kind.
if grep -nI -- '-----BEGIN .*PRIVATE KEY-----' "${FILES[@]}"; then
    report "a private key is in a tracked file"
fi

# An AWS access key identifier.
if grep -nEI '(^|[^A-Z0-9])AKIA[A-Z0-9]{16}([^A-Z0-9]|$)' "${FILES[@]}"; then
    report "an AWS access key is in a tracked file"
fi

if [ "${STATUS}" -eq 0 ]; then
    echo "check-secrets: clean (${#FILES[@]} files)"
fi

exit "${STATUS}"
