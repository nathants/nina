#!/usr/bin/env bash
# Run all integration tests with integration build tag
set -eou pipefail

export MODEL="${MODEL:-o4-mini-flex}"

echo "Running integration tests..."

# Build and run integration tests with the integration tag
if [ -f "/tmp/nina-integration.test" ]; then
    rm /tmp/nina-integration.test
fi

go test -tags=integration ./integration -race -o /tmp/nina-integration.test -c
if [ -f "/tmp/nina-integration.test" ]; then
    /tmp/nina-integration.test -test.v -test.count=1
else
    echo "No integration tests found"
fi
