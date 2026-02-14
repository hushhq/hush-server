#!/bin/bash

# Test script for Matrix chat persistence and encryption
# Verifies that chat messages persist in Matrix and E2EE is enabled

set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
MATRIX_URL="${MATRIX_URL:-http://localhost:8008}"
MAX_WAIT_SECONDS=120
POLL_INTERVAL=5

# Test result tracking
TESTS_PASSED=0
TESTS_FAILED=0

# Global variables for test state
ACCESS_TOKEN=""
USER_ID=""
ROOM_ID=""
MESSAGE_TXN_ID="test-$(date +%s)"
TEST_MESSAGE="Test message for persistence verification at $(date +%Y-%m-%dT%H:%M:%S)"

# Logging functions
log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

# Test 1: Wait for Synapse to be ready
test_synapse_health() {
    log_info "Test 1: Waiting for Synapse health check..."

    local elapsed=0
    while [ $elapsed -lt $MAX_WAIT_SECONDS ]; do
        if curl -f -s "${MATRIX_URL}/health" > /dev/null 2>&1; then
            log_pass "Synapse is healthy and ready"
            return 0
        fi

        sleep $POLL_INTERVAL
        elapsed=$((elapsed + POLL_INTERVAL))
        echo -n "."
    done

    echo ""
    log_fail "Synapse did not become healthy within ${MAX_WAIT_SECONDS}s"
    return 1
}

# Test 2: Register guest user
test_guest_registration() {
    log_info "Test 2: Registering guest user..."

    local response
    local http_code

    response=$(curl -s -w "\n%{http_code}" -X POST \
        "${MATRIX_URL}/_matrix/client/v3/register?kind=guest" \
        -H "Content-Type: application/json" \
        -d '{}' 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "200" ]; then
        # Extract access_token and user_id using grep and sed
        ACCESS_TOKEN=$(echo "$body" | grep -o '"access_token":"[^"]*"' | sed 's/"access_token":"\([^"]*\)"/\1/')
        USER_ID=$(echo "$body" | grep -o '"user_id":"[^"]*"' | sed 's/"user_id":"\([^"]*\)"/\1/')

        if [ -n "$ACCESS_TOKEN" ] && [ -n "$USER_ID" ]; then
            log_pass "Guest user registered: $USER_ID"
            return 0
        else
            log_fail "Guest registration succeeded but failed to extract credentials"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Guest registration failed with status $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Test 3: Create encrypted room
test_create_encrypted_room() {
    log_info "Test 3: Creating room with encryption enabled..."

    if [ -z "$ACCESS_TOKEN" ]; then
        log_fail "Cannot create room: no access token available"
        return 1
    fi

    local response
    local http_code

    # Create room with encryption enabled via initial_state
    response=$(curl -s -w "\n%{http_code}" -X POST \
        "${MATRIX_URL}/_matrix/client/v3/createRoom" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" \
        -H "Content-Type: application/json" \
        -d '{
            "preset": "private_chat",
            "initial_state": [
                {
                    "type": "m.room.encryption",
                    "state_key": "",
                    "content": {
                        "algorithm": "m.megolm.v1.aes-sha2"
                    }
                }
            ]
        }' 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "200" ]; then
        ROOM_ID=$(echo "$body" | grep -o '"room_id":"[^"]*"' | sed 's/"room_id":"\([^"]*\)"/\1/')

        if [ -n "$ROOM_ID" ]; then
            log_pass "Encrypted room created: $ROOM_ID"
            return 0
        else
            log_fail "Room creation succeeded but failed to extract room_id"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Room creation failed with status $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Test 4: Send test message
test_send_message() {
    log_info "Test 4: Sending test message to room..."

    if [ -z "$ACCESS_TOKEN" ] || [ -z "$ROOM_ID" ]; then
        log_fail "Cannot send message: missing access token or room ID"
        return 1
    fi

    local response
    local http_code

    response=$(curl -s -w "\n%{http_code}" -X PUT \
        "${MATRIX_URL}/_matrix/client/v3/rooms/${ROOM_ID}/send/m.room.message/${MESSAGE_TXN_ID}" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "{
            \"msgtype\": \"m.text\",
            \"body\": \"${TEST_MESSAGE}\"
        }" 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "200" ]; then
        local event_id=$(echo "$body" | grep -o '"event_id":"[^"]*"' | sed 's/"event_id":"\([^"]*\)"/\1/')
        if [ -n "$event_id" ]; then
            log_pass "Test message sent successfully: $event_id"
            return 0
        else
            log_fail "Message send succeeded but no event_id returned"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Message send failed with status $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Test 5: Verify message persistence
test_message_persistence() {
    log_info "Test 5: Verifying message persists in room timeline..."

    if [ -z "$ACCESS_TOKEN" ] || [ -z "$ROOM_ID" ]; then
        log_fail "Cannot fetch timeline: missing access token or room ID"
        return 1
    fi

    local response
    local http_code

    response=$(curl -s -w "\n%{http_code}" -X GET \
        "${MATRIX_URL}/_matrix/client/v3/rooms/${ROOM_ID}/messages?dir=b&limit=10" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "200" ]; then
        # Check if our test message appears in the timeline
        if echo "$body" | grep -q "${TEST_MESSAGE}"; then
            log_pass "Test message found in room timeline (persistence verified)"
            return 0
        else
            log_fail "Test message not found in room timeline"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Timeline fetch failed with status $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Test 6: Verify encryption state event
test_encryption_state() {
    log_info "Test 6: Verifying room has E2EE encryption enabled..."

    if [ -z "$ACCESS_TOKEN" ] || [ -z "$ROOM_ID" ]; then
        log_fail "Cannot check encryption: missing access token or room ID"
        return 1
    fi

    local response
    local http_code

    response=$(curl -s -w "\n%{http_code}" -X GET \
        "${MATRIX_URL}/_matrix/client/v3/rooms/${ROOM_ID}/state/m.room.encryption" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "200" ]; then
        # Check if algorithm is m.megolm.v1.aes-sha2
        if echo "$body" | grep -q '"algorithm":"m.megolm.v1.aes-sha2"'; then
            log_pass "Room encryption verified: m.megolm.v1.aes-sha2"
            return 0
        else
            log_fail "Room has encryption but wrong algorithm or missing"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Encryption state fetch failed with status $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Main execution
main() {
    echo "=========================================="
    echo "  Matrix Chat Persistence & E2EE Test"
    echo "=========================================="
    echo "Target: ${MATRIX_URL}"
    echo ""

    # Run all tests in sequence (each depends on previous)
    test_synapse_health || exit 1
    test_guest_registration || exit 1
    test_create_encrypted_room || exit 1
    test_send_message || exit 1
    test_message_persistence || exit 1
    test_encryption_state || exit 1

    # Summary
    echo ""
    echo "=========================================="
    echo "  Test Summary"
    echo "=========================================="
    echo -e "Passed: ${GREEN}${TESTS_PASSED}${NC}"
    echo -e "Failed: ${RED}${TESTS_FAILED}${NC}"
    echo ""

    # Exit with appropriate code
    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}All tests passed!${NC}"
        echo ""
        echo "Verified:"
        echo "  ✓ Chat messages persist in Matrix (not ephemeral)"
        echo "  ✓ Rooms created with E2EE enabled (m.megolm.v1.aes-sha2)"
        exit 0
    else
        echo -e "${RED}Some tests failed.${NC}"
        exit 1
    fi
}

# Run main function
main
