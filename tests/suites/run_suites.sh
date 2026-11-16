#!/usr/bin/env bash
# Runs every suite in this folder against one server (CI runs it; tests/README.md says how to run it yourself). Each suite exits
# non-zero on a failed check; this script exits non-zero when any suite did, and lists them.
#
#   SERVER_BIN    a built server (go build -o /tmp/agentrpg ./cmd/server). With it, this script starts the server itself, on PORT
#                 (default 8099) with DATABASE_URL and AGENTRPG_TEST_CLOCK=1, and restarts it before each group of suites: the
#                 server allows 20 new accounts an hour from one address. Its log is OUT/server.log, and a query sent outside
#                 its own transaction in it ("query outside its transaction") fails the run.
#   BASE          without SERVER_BIN: the server to test (default http://localhost:8099), used as it is (no restarts, so a run
#                 of every suite needs the registration limit reset in between: run a few suites at a time with SUITES).
#   SUITES        only these suites (names without .py, space-separated), in this script's order.
#   OUT           where the results go (default ./suite-results): one file per suite and summary.txt.
#   ARPG_LOG_DIR  where the suites write their request logs (default: OUT/logs).
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$PWD/suite-results}"
PORT="${PORT:-8099}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
export ARPG_LOG_DIR="${ARPG_LOG_DIR:-$OUT/logs}"
mkdir -p "$ARPG_LOG_DIR"
rm -f "$OUT/summary.txt"
if [ -n "${SERVER_BIN:-}" ]; then
  export BASE="http://localhost:$PORT"
  : "${DATABASE_URL:?DATABASE_URL is needed with SERVER_BIN}"
else
  export BASE="${BASE:-http://localhost:8099}"
fi
SERVER_PID=""

stop_server() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
    SERVER_PID=""
  fi
}

# restart: a fresh server (the registration limit starts again). Without SERVER_BIN, nothing.
restart() {
  [ -n "${SERVER_BIN:-}" ] || return 0
  stop_server
  AGENTRPG_TEST_CLOCK=1 DATABASE_URL="$DATABASE_URL" PORT="$PORT" "$SERVER_BIN" >> "$OUT/server.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 180); do # the first start loads the SRD data into an empty database
    if curl -sf "$BASE/api/version" > /dev/null; then
      return 0
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "the server exited; the end of its log:" >&2
      tail -30 "$OUT/server.log" >&2
      exit 2
    fi
    sleep 1
  done
  echo "the server didn't answer on $BASE within 180 seconds" >&2
  tail -30 "$OUT/server.log" >&2
  exit 2
}
trap stop_server EXIT

wanted() { [ -z "${SUITES:-}" ] || [[ " $SUITES " == *" $1 "* ]]; }

# run SCRIPT NAME [ARGS...]: one suite, its output in OUT/NAME.txt
run() {
  local script="$1" name="$2"
  shift 2
  wanted "$name" || return 0
  local t0=$SECONDS
  python3 "$HERE/$script.py" "$@" > "$OUT/$name.txt" 2>&1
  local code=$?
  echo "$name exit $code ($((SECONDS - t0))s)" >> "$OUT/summary.txt"
  echo "$name exit $code ($((SECONDS - t0))s)"
}

# the level runs of a group, at once (each makes one account)
levels() {
  local spec
  for spec in "$@"; do
    set -- $spec
    run levelrun "level_$1" "$1" "$2" "$3" &
  done
  wait
}

# The groups as the notes' staging batch runs them, then the suites it doesn't list.
restart
run test_builds builds12
run test_builds2 builds48
run test_wrong wrong
run test_after1 after1
levels "barbarian soldier dwarf" "bard acolyte human" "cleric acolyte dwarf" "druid sage elf" "fighter soldier goliath" \
  "monk criminal halfling" "paladin soldier dragonborn"
restart
levels "ranger criminal elf" "rogue criminal halfling" "sorcerer sage tiefling" "warlock criminal tiefling" "wizard sage gnome"
run shoprun shop
restart
run levelup_fixes_test levelup_fixes
run expertise_rule_test expertise
run feats_rule_test feats
for pair in request_rule_test:request_rule gm_campaign_rule_test:gm_campaign_rule feat_choices_rule_test:feat_choices_rule \
  seen_fixes_test:seen_fixes access_rule_test:access_rule invocation_repeat_test:invocation_repeat start_rule_test:start_rule \
  action_log_rule_test:action_log_rule kick_character_test:kick_character_test dodge_test:dodge_test \
  dodge_spell_attack_test:dodge_spell_attack_test phase0_rule_test:phase0_rule ownership_rule_test:ownership_rule \
  progression_race_test:progression_race action_types_rule_test:action_types_rule late_gm_test:late_gm \
  turn_schedule_rule_test:turn_schedule_rule expected_turn_test:expected_turn; do
  wanted "${pair#*:}" || continue
  restart
  run "${pair%%:*}" "${pair#*:}"
done
stop_server

status=0
failed="$(grep -v ' exit 0 ' "$OUT/summary.txt" | cut -d' ' -f1)"
if [ -n "$failed" ]; then
  status=1
  for name in $failed; do
    echo "=== $name failed; the end of its output:"
    tail -25 "$OUT/$name.txt"
  done
fi
if [ -n "${SERVER_BIN:-}" ]; then
  outside="$(grep -c 'query outside its transaction' "$OUT/server.log" || true)"
  echo "queries outside their transaction in the server log: $outside" | tee -a "$OUT/summary.txt"
  if [ "$outside" != "0" ]; then
    grep -A3 'query outside its transaction' "$OUT/server.log" | head -40
    status=1
  fi
fi
echo "DONE (exit $status)" >> "$OUT/summary.txt"
exit $status
