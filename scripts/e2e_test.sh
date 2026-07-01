#!/usr/bin/env bash
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
SUPPLIER_NAME="e2e-test-$$"
PASS=0
FAIL=0

cleanup() {
  echo ""
  echo "=== Cleanup ==="
  if [ -n "${SUPPLIER_NAME:-}" ]; then
    curl -s -o /dev/null -X DELETE "$BASE_URL/api/v1/suppliers/$SUPPLIER_NAME" || true
  fi
  if [ -n "${SERVER_PID:-}" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f /tmp/e2e_req_$$.json /tmp/e2e_port_$$.txt
}

trap cleanup EXIT

pass() { echo "  ✅ $1"; ((PASS++)); }
fail() { echo "  ❌ $1"; ((FAIL++)); }

echo "================================================"
echo "  E2E Test: Notification Delivery"
echo "================================================"
echo ""

# ---- 1. Health check ----
echo "--- Step 1: Server health check ---"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/suppliers" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" != "200" ]; then
  fail "Server not reachable at $BASE_URL (HTTP $HTTP_CODE)"
  exit 1
fi
pass "Server is reachable at $BASE_URL"

# ---- 2. Start local test server on random port ----
echo ""
echo "--- Step 2: Start local test server ---"

# Start Python HTTP server that listens until it receives one POST request
python3 -c "
import http.server, json, os, socket

# Find an available port
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(('', 0))
port = s.getsockname()[1]
s.close()

# Write port to file so the shell script can read it
with open('/tmp/e2e_port_$$.txt', 'w') as f:
    f.write(str(port))

class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        cl = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(cl)
        with open('/tmp/e2e_req_$$.json', 'w') as f:
            json.dump({
                'method': self.command,
                'path': self.path,
                'headers': dict(self.headers),
                'body': body.decode()
            }, f, indent=2)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'OK')
    def log_message(self, *a): pass

srv = http.server.HTTPServer(('', port), Handler)
srv.timeout = 60
while True:
    srv.handle_request()
" &

SERVER_PID=$!

# Wait for server to start and get its port
for i in $(seq 1 10); do
  if [ -f /tmp/e2e_port_$$.txt ]; then
    TEST_PORT=$(cat /tmp/e2e_port_$$.txt)
    break
  fi
  sleep 0.3
done

if [ -z "${TEST_PORT:-}" ]; then
  fail "Test server failed to start"
  exit 1
fi

# Verify server is listening
sleep 0.5
if ! curl -s --connect-timeout 2 "http://localhost:$TEST_PORT" >/dev/null 2>&1; then
  # The server only handles POST, so this "failing" is normal.
  # Check that the process is alive instead.
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "Test server process died unexpectedly"
    exit 1
  fi
fi
pass "Test server listening on port $TEST_PORT"

# ---- 3. Create supplier ----
echo ""
echo "--- Step 3: Create supplier '$SUPPLIER_NAME' ---"
SUPPLIER_RESP=$(curl -s -X POST "$BASE_URL/api/v1/suppliers" \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"$SUPPLIER_NAME\",
    \"url\": \"http://localhost:$TEST_PORT/notify\",
    \"method\": \"POST\",
    \"headers\": {\"X-Custom\": \"e2e-test-value\"},
    \"retry\": {\"max_attempts\": 1, \"base_delay\": \"1s\", \"max_delay\": \"5s\"},
    \"accepted_statuses\": [200]
  }")
SUPPLIER_ID=$(echo "$SUPPLIER_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
if [ -z "$SUPPLIER_ID" ]; then
  fail "Failed to create supplier: $(echo "$SUPPLIER_RESP" | head -c 200)"
  exit 1
fi
pass "Supplier created (id=$SUPPLIER_ID)"

# ---- 4. Submit notification ----
echo ""
echo "--- Step 4: Submit notification ---"
NOTIF_RESP=$(curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -d "{
    \"supplier\": \"$SUPPLIER_NAME\",
    \"body\": {\"order_id\": \"12345\", \"event\": \"payment_success\"}
  }")
NOTIF_ID=$(echo "$NOTIF_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
NOTIF_STATUS=$(echo "$NOTIF_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])" 2>/dev/null || echo "")
if [ "$NOTIF_STATUS" != "accepted" ]; then
  fail "Notification submission failed: $(echo "$NOTIF_RESP" | head -c 200)"
  exit 1
fi
pass "Notification accepted (id=$NOTIF_ID)"

# ---- 5. Wait for delivery ----
echo ""
echo "--- Step 5: Wait for delivery ---"
FINAL_STATUS=""
for i in $(seq 1 10); do
  sleep 1
  CUR=$(curl -s "$BASE_URL/api/v1/notifications/$NOTIF_ID" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo "unknown")
  echo "  Attempt $i: status = $CUR"
  if [ "$CUR" = "delivered" ] || [ "$CUR" = "dead" ]; then
    FINAL_STATUS="$CUR"
    break
  fi
done

# ---- 6. Verify notification status ----
echo ""
echo "--- Step 6: Verify notification status ---"
if [ "$FINAL_STATUS" = "delivered" ]; then
  pass "Notification delivered successfully"
elif [ "$FINAL_STATUS" = "dead" ]; then
  fail "Notification went to dead letter queue"
else
  fail "Notification still pending after timeout (status=${FINAL_STATUS:-unknown})"
fi

NOTIF_DETAIL=$(curl -s "$BASE_URL/api/v1/notifications/$NOTIF_ID")
echo ""
echo "  Full notification:"
echo "$NOTIF_DETAIL" | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

ATTEMPT_COUNT=$(echo "$NOTIF_DETAIL" | python3 -c "import sys,json; print(json.load(sys.stdin)['attempt_count'])" 2>/dev/null || echo "?")
if [ "$ATTEMPT_COUNT" = "1" ]; then
  pass "Exactly 1 delivery attempt (no unnecessary retries)"
else
  echo "  ⚠️  Delivery attempts: $ATTEMPT_COUNT"
fi

# ---- 7. Verify captured request ----
echo ""
echo "--- Step 7: Verify captured request ---"
if [ ! -f /tmp/e2e_req_$$.json ]; then
  fail "No request was captured by the test server"
else
  pass "Request captured by test server"
  echo ""
  echo "  Captured payload:"
  python3 -m json.tool /tmp/e2e_req_$$.json 2>/dev/null | sed 's/^/  /'

  CAP_METHOD=$(python3 -c "import sys,json; print(json.load(open('/tmp/e2e_req_$$.json'))['method'])" 2>/dev/null)
  CAP_PATH=$(python3 -c "import sys,json; print(json.load(open('/tmp/e2e_req_$$.json'))['path'])" 2>/dev/null)
  CAP_HEADER=$(python3 -c "import sys,json; print(json.load(open('/tmp/e2e_req_$$.json'))['headers'].get('X-Custom',''))" 2>/dev/null)
  CAP_BODY=$(python3 -c "import sys,json; print(json.load(open('/tmp/e2e_req_$$.json'))['body'])" 2>/dev/null)

  [ "$CAP_METHOD" = "POST" ] && pass "Method is POST" || fail "Method mismatch: $CAP_METHOD"
  [ "$CAP_PATH" = "/notify" ] && pass "Path is /notify" || fail "Path mismatch: $CAP_PATH"
  [ "$CAP_HEADER" = "e2e-test-value" ] && pass "Custom header X-Custom forwarded correctly" || fail "Header mismatch: $CAP_HEADER"
  echo "$CAP_BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d.get('order_id')=='12345'; assert d.get('event')=='payment_success'" 2>/dev/null \
    && pass "Body matches expected payload" \
    || fail "Body mismatch: $CAP_BODY"
fi

# ---- Summary ----
echo ""
echo "================================================"
echo "  Results: $PASS passed, $FAIL failed"
echo "================================================"

[ "$FAIL" -eq 0 ]
