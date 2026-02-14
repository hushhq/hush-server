#!/bin/bash

# Test script for Synapse Matrix homeserver
# Verifies that Synapse is running and functional

set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
SYNAPSE_URL="${SYNAPSE_URL:-http://localhost:8008}"
MAX_WAIT_SECONDS=120
POLL_INTERVAL=5

# Test result tracking
TESTS_PASSED=0
TESTS_FAILED=0

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
test_health_check() {
    log_info "Test 1: Waiting for Synapse health check..."

    local elapsed=0
    while [ $elapsed -lt $MAX_WAIT_SECONDS ]; do
        if curl -f -s "${SYNAPSE_URL}/health" > /dev/null 2>&1; then
            log_pass "Synapse health check endpoint is responding"
            return 0
        fi

        sleep $POLL_INTERVAL
        elapsed=$((elapsed + POLL_INTERVAL))
        echo -n "."
    done

    echo ""
    log_fail "Synapse health check endpoint did not respond within ${MAX_WAIT_SECONDS}s"
    return 1
}

# Test 2: Verify /_matrix/client/versions endpoint
test_client_versions() {
    log_info "Test 2: Testing /_matrix/client/versions endpoint..."

    local response
    response=$(curl -f -s "${SYNAPSE_URL}/_matrix/client/versions" 2>&1)
    local exit_code=$?

    if [ $exit_code -ne 0 ]; then
        log_fail "Failed to connect to /_matrix/client/versions endpoint"
        return 1
    fi

    # Check if response contains "versions" key (basic JSON validation)
    if echo "$response" | grep -q '"versions"'; then
        log_pass "/_matrix/client/versions returns valid response"
        return 0
    else
        log_fail "/_matrix/client/versions response format invalid"
        echo "Response: $response"
        return 1
    fi
}

# Test 3: Test guest registration
test_guest_registration() {
    log_info "Test 3: Testing guest registration flow..."

    # Attempt guest registration
    local response
    local http_code

    response=$(curl -s -w "\n%{http_code}" -X POST \
        "${SYNAPSE_URL}/_matrix/client/v3/register?kind=guest" \
        -H "Content-Type: application/json" \
        -d '{}' 2>&1)

    http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # Guest registration should return 200 (success) or 403 (disabled)
    # We check if the endpoint is reachable and responds correctly
    if [ "$http_code" = "200" ]; then
        # Check if response contains required fields
        if echo "$body" | grep -q '"access_token"' && echo "$body" | grep -q '"user_id"'; then
            log_pass "Guest registration is enabled and working"
            return 0
        else
            log_fail "Guest registration returned 200 but missing required fields"
            echo "Response: $body"
            return 1
        fi
    elif [ "$http_code" = "403" ]; then
        # Guest registration is disabled, but endpoint is working
        if echo "$body" | grep -q '"errcode"'; then
            log_pass "Guest registration endpoint is responding (disabled by config)"
            return 0
        else
            log_fail "Guest registration returned 403 with unexpected format"
            echo "Response: $body"
            return 1
        fi
    else
        log_fail "Guest registration endpoint returned unexpected status: $http_code"
        echo "Response: $body"
        return 1
    fi
}

# Main execution
main() {
    echo "=========================================="
    echo "  Synapse Matrix Homeserver Test Suite"
    echo "=========================================="
    echo "Target: ${SYNAPSE_URL}"
    echo ""

    # Run all tests
    test_health_check || true
    test_client_versions || true
    test_guest_registration || true

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
        exit 0
    else
        echo -e "${RED}Some tests failed.${NC}"
        exit 1
    fi
}

# Run main function
main
