#!/usr/bin/env bash
# Sends test requests from the client pod and compares the answers.
# Usage: bash deploy/demo/test.sh [routing-key]   (default demo1)
set -u

KEY=${1:-demo1}

pass=0
fail=0

req() { kubectl exec -n demo client -- curl -s -m 5 "$@" echo:8080; }

check() {
  local name=$1 want=$2; shift 2
  local got
  got=$(req "$@" | tr -d '\r\n')
  if [ "$got" = "$want" ]; then
    pass=$((pass + 1)); echo "PASS  $name: $got"
  else
    fail=$((fail + 1)); echo "FAIL  $name: got '$got', want '$want'"
  fi
}

# check NAME WANT [N] ARGS... sends N requests (default 1); every answer must match.
check_n() {
  local name=$1 want=$2 n=$3; shift 3
  local i
  for i in $(seq "$n"); do
    got=$(req "$@" | tr -d '\r\n')
    if [ "$got" != "$want" ]; then
      fail=$((fail + 1)); echo "FAIL  $name (request $i): got '$got', want '$want'"
      return
    fi
  done
  pass=$((pass + 1)); echo "PASS  $name: $n x $want"
}

check_n "T1 no key"              "baseline"   10
check_n "T2 key $KEY"           "fork $KEY" 10 -H "baggage: routing-key=$KEY"
check   "T3 key among others"    "fork $KEY" -H "baggage: foo=bar, routing-key=$KEY;p=1"
check   "T4 near miss ${KEY%?}"    "baseline"   -H "baggage: routing-key=${KEY%?}"
check   "T4 near miss ${KEY}2"    "baseline"   -H "baggage: routing-key=${KEY}2"
check   "T4 other baggage entry" "baseline"   -H "baggage: other-routing-key=$KEY"

echo
echo "$pass passed, $fail failed"
echo "Fail-loud test (needs scale rights):"
echo "  kubectl scale -n demo deploy/echo-$KEY --replicas=0"
echo "  kubectl exec -n demo client -- curl -s -o /dev/null -w '%{http_code}\n' -H 'baggage: routing-key=$KEY' echo:8080   # want 503"
echo "  kubectl scale -n demo deploy/echo-$KEY --replicas=1"
[ "$fail" -eq 0 ]
