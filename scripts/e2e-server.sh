#!/usr/bin/env bash
# End-to-end test of the Kirch server with real binaries:
#   two people push / clone / pull over kirch:// (TCP + TLS),
#   git checks the server's repository,
#   and (when KIRCH_DATABASE_URL is set) the HTTP API shows the commits.
#
#   scripts/e2e-server.sh                         # push / pull only
#   KIRCH_DATABASE_URL=postgres://... scripts/e2e-server.sh   # + indexer + API
#   E2E_DOCKER=1 scripts/e2e-server.sh            # against docker compose (own project, removed after)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
COMPOSE=(docker compose -f "$ROOT/compose.yaml" -p kirch-e2e)
cleanup() {
  kill "${SERVER_PID:-}" 2>/dev/null || true
  wait 2>/dev/null || true
  if [ "${E2E_DOCKER:-}" = 1 ]; then "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; [ -f "$WORK/server.log" ] && tail -20 "$WORK/server.log" >&2; exit 1; }

step "build"
(cd "$ROOT" && go build -o "$WORK/bin/kirch" . && go build -o "$WORK/bin/kirch-server" ./server)
K="$WORK/bin/kirch"

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }

if [ "${E2E_DOCKER:-}" = 1 ]; then
  step "start docker compose (project kirch-e2e)"
  "${COMPOSE[@]}" up -d --build --wait >/dev/null 2>"$WORK/compose.log" || { cat "$WORK/compose.log"; fail "compose up"; }
  TCP_PORT=9418; HTTP_PORT=8080
  "${COMPOSE[@]}" cp server:/data/tls/cert.pem "$WORK/cert.pem" >/dev/null
  export KIRCH_CA_FILE="$WORK/cert.pem"
  KIRCH_DATABASE_URL=docker
  server_repo() { rm -rf "$WORK/srvcopy"; "${COMPOSE[@]}" cp "server:/data/repos/evin/servo-board/.kirch" "$WORK/srvcopy" >/dev/null; echo "$WORK/srvcopy"; }
else
  TCP_PORT=$(free_port); HTTP_PORT=$(free_port)
  step "start server (tcp :$TCP_PORT, http :$HTTP_PORT)"
  KIRCH_DATA_DIR="$WORK/data" KIRCH_TCP_ADDR="127.0.0.1:$TCP_PORT" KIRCH_HTTP_ADDR="127.0.0.1:$HTTP_PORT" \
    "$WORK/bin/kirch-server" >"$WORK/server.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 50); do
    [ -f "$WORK/data/tls/cert.pem" ] && grep -q "listening" "$WORK/server.log" && break
    sleep 0.1
  done
  grep -q "kirch:// listening" "$WORK/server.log" || fail "server did not start"
  export KIRCH_CA_FILE="$WORK/data/tls/cert.pem"
  server_repo() { echo "$WORK/data/repos/evin/servo-board/.kirch"; }
fi
URL="kirch://127.0.0.1:$TCP_PORT/evin/servo-board"

netlist() { printf '{"format":"kirch.netlist/1","nets":{"GND":["SERVO1.GND","U1.GND"],"SIG":["SERVO1.SIG","%s"]}}' "$1" > netlist.json; }
who() { "$K" config user.name "$1" >/dev/null; "$K" config user.email "$2" >/dev/null; }

step "evin: init, commit, push to $URL"
mkdir "$WORK/evin" && cd "$WORK/evin"
"$K" init >/dev/null && who Evin evin@example.com
netlist U1.GPIO9; mkdir firmware; echo 'void setup() {}' > firmware/main.cpp
"$K" add . >/dev/null && "$K" commit -m "Servo signal on GPIO9" >/dev/null
"$K" remote add origin "$URL"
"$K" push | tee "$WORK/push1.txt"
grep -q "\[new branch\]" "$WORK/push1.txt" || fail "first push"

step "hridhayesh: clone over kirch://"
cd "$WORK" && "$K" clone "$URL" hb >/dev/null
cd "$WORK/hb" && who Hridhayesh hb@example.com
cmp -s firmware/main.cpp "$WORK/evin/firmware/main.cpp" || fail "cloned files differ"

step "hridhayesh: rewire servo, push"
netlist U1.GPIO10; "$K" add . >/dev/null && "$K" commit -m "Move servo to GPIO10" >/dev/null
"$K" push >/dev/null
HB_TIP=$("$K" log -n 1 --oneline | cut -c1-7)

step "evin: pull (fast-forward) and see the wiring diff"
cd "$WORK/evin"
"$K" pull | tee "$WORK/pull.txt"
grep -q "Fast-forward" "$WORK/pull.txt" || fail "pull was not a fast-forward"
"$K" diff HEAD~1 HEAD | tee "$WORK/diff.txt"
grep -q "SERVO1.SIG: U1.GPIO9 → U1.GPIO10" "$WORK/diff.txt" || fail "wiring diff"

step "diverge: evin pushes, hridhayesh's push is refused, then pull + push works"
netlist U1.GPIO11; "$K" add . >/dev/null && "$K" commit -m "GPIO11" >/dev/null && "$K" push >/dev/null
cd "$WORK/hb"; echo '// note' >> firmware/main.cpp; "$K" add . >/dev/null && "$K" commit -m "Comment" >/dev/null
if "$K" push 2>"$WORK/refused.txt"; then fail "non-fast-forward push was accepted"; fi
grep -q "pull first" "$WORK/refused.txt" || fail "refusal message: $(cat "$WORK/refused.txt")"
"$K" pull >/dev/null && "$K" push >/dev/null
"$K" log --oneline | head -5

step "git checks the server's copy"
SRV="$(server_repo)"
git --git-dir="$SRV" fsck --strict
[ "$(git --git-dir="$SRV" rev-parse main)" = "$(cd "$WORK/hb" && git --git-dir=.kirch rev-parse main)" ] || fail "server main != hb main"

step "wrong certificate is refused"
if KIRCH_CA_FILE= "$K" fetch 2>"$WORK/tls.txt"; then fail "untrusted certificate accepted"; fi
grep -qi "certificate" "$WORK/tls.txt" || fail "tls error: $(cat "$WORK/tls.txt")"

if [ -n "${KIRCH_DATABASE_URL:-}" ]; then
  API="http://127.0.0.1:$HTTP_PORT"
  step "HTTP API: health, repos, commits, diff"
  curl -fsS "$API/healthz"; echo
  curl -fsS "$API/api/repos" | tee "$WORK/repos.json"; echo
  grep -q '"servo-board"' "$WORK/repos.json" || fail "repo not listed"
  curl -fsS "$API/api/repos/evin/servo-board/commits?limit=10" > "$WORK/commits.json"
  python3 - "$WORK/commits.json" "$WORK/hb" "$K" <<'PY'
import json, subprocess, sys
api = [c["id"] for c in json.load(open(sys.argv[1]))["commits"]]
cli = subprocess.run([sys.argv[3], "log", "-n", "10"], cwd=sys.argv[2], capture_output=True, text=True).stdout
cli = [l.split()[1] for l in cli.splitlines() if l.startswith("commit ")]
assert api == cli, f"API order {api} != kirch log {cli}"
print(f"commits match kirch log: {len(api)}")
PY
  C2=$(cd "$WORK/evin" && git --git-dir=.kirch log --format=%H --grep "Move servo" -n 1)
  curl -fsS "$API/api/repos/evin/servo-board/commits/$C2/diff" | tee "$WORK/cdiff.json"; echo
  grep -q '"text":"SERVO1.SIG: U1.GPIO9 → U1.GPIO10"' "$WORK/cdiff.json" || fail "API wiring diff"
  code=$(curl -s -o /dev/null -w '%{http_code}' "$API/api/repos/evin/nope")
  [ "$code" = 404 ] || fail "unknown repo gave $code"
fi

step "PASS"
