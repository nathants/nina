#!/usr/bin/env bash
# Run a single integration test with streaming debug output
set -eou pipefail

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
    echo "Usage: $0 TestName [TableCaseName]"
    echo "Example: $0 TestLoopSimpleEdit"
    echo "Example: $0 TestLoopSimpleEdit edit_readme"
    echo "Example: $0 TestLoopMultiLineEdit/replace_multiple_lines_in_go_file"
    exit 1
fi

TEST_NAME="$1"
TABLE_CASE="${2:-}"

# If table case is provided as second argument, combine them
if [ -n "$TABLE_CASE" ]; then
    TEST_NAME="${TEST_NAME}/${TABLE_CASE}"
fi

export MODEL="${MODEL:-o4-mini-flex}"

echo "Running integration test: $TEST_NAME"
echo "Model: $MODEL"
echo

# Build and run the specific integration test with streaming output
if [ -f "/tmp/nina-integration-one.test" ]; then
    rm /tmp/nina-integration-one.test
fi

go test -tags=integration ./integration -race -o /tmp/nina-integration-one.test -c
if [ -f "/tmp/nina-integration-one.test" ]; then
    # Use a more flexible regex that properly handles table test names
    /tmp/nina-integration-one.test -test.v -test.run "^${TEST_NAME}$" -test.count=1
else
    echo "Failed to build integration test binary"
    exit 1
fi
