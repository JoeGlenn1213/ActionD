#!/bin/bash
set -e

echo "🚀 Starting ActionD Integration Test"

# cleanup
pkill actiond || true
lsof -ti:3000 | xargs kill -9 || true
lgh serve > /dev/null 2>&1 &
echo "✅ LGH Server started"

# Build
cd ../
go build -o actiond ./cmd/actiond/
echo "✅ ActionD built"

# Setup test DB
TEST_DB_DIR="$HOME/.localgithub/actions_test"
rm -rf "$TEST_DB_DIR"
mkdir -p "$TEST_DB_DIR"

# Modify main.go to use test DB?
# Ideally we should pass config, but for now we rely on env vars or just use the default
# Since we can't easily injection config without refactor, we will just use the default DB 
# and clean it up or accept it appends. Using default for now.

# Start ActionD
./actiond --repo-root $(pwd)/../ > actiond_test.log 2>&1 &
ACTIOND_PID=$!
echo "✅ ActionD started (PID: $ACTIOND_PID) with repo-root: $(pwd)/../"

# Wait for startup
sleep 5

# Trigger Event
# Trigger Event
echo "📨 Triggering 'git.push' event for ActionD..."
# Use curl to inject event manually so we can control repo name (ActionD vs ActionD.git)
curl -s -X POST http://localhost:9418/replay \
  -H "Content-Type: application/json" \
  -d '{"type": "git.push", "repo": "ActionD", "payload": {"ref": "refs/heads/main", "commit": "test-commit"}}' || echo "curl failed"

# Wait for processing (go-build takes time)
echo "⏳ Waiting for processing (20s)..."
sleep 20

# Verify API
echo "🔍 Verifying API..."
RESPONSE=$(curl -s http://localhost:3000/api/actions)
echo "API Response: $RESPONSE"

if echo "$RESPONSE" | grep -q "echo"; then
    echo "✅ found 'echo' job in API response"
else
    echo "❌ 'echo' job NOT found in API response"
    kill $ACTIOND_PID
    exit 1
fi

if echo "$RESPONSE" | grep -q "done"; then
    echo "✅ Job status is 'done'"
else
    echo "❌ Job status is NOT 'done'"
    kill $ACTIOND_PID
    exit 1
fi

# Cleanup
echo "📝 ActionD Logs:"
cat actiond_test.log || echo "Log file not found"

kill $ACTIOND_PID
echo "✅ Test Passed!"
