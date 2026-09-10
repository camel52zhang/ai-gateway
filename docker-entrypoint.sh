#!/bin/sh
set -e

# The data directory (/app/data) is usually a bind mount from the host. When the
# host directory was created by root, it — and the SQLite database file inside
# it — are owned by root. The gateway runs as the unprivileged 'app' user, which
# then cannot write the database at all, and every write fails with
# "attempt to write a readonly database (8)". Fix the ownership at startup (a
# no-op when already correct) so redeploys never silently break on a read-only
# DB. Then drop privileges to 'app'.
if [ "$(id -u)" = "0" ]; then
  chown -R app:app /app/data 2>/dev/null || true
  exec su-exec app /app/ai-gateway "$@"
fi

exec /app/ai-gateway "$@"
