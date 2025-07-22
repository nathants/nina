set -uo pipefail

install_if_missing() {
  local bin=$1
  local pkg=$2

  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "Installing ${bin} (${pkg}) ..."
    ( cd ~ && go install "${pkg}" )
  fi
}

install_if_missing staticcheck   honnef.co/go/tools/cmd/staticcheck@latest
install_if_missing ineffassign   github.com/gordonklaus/ineffassign@latest
install_if_missing errcheck      github.com/kisielk/errcheck@latest
install_if_missing bodyclose     github.com/timakin/bodyclose@latest
install_if_missing nargs         github.com/alexkohler/nargs/cmd/nargs@latest
install_if_missing exhaustive    github.com/nishanths/exhaustive/cmd/exhaustive@latest
install_if_missing go-hasdefer   github.com/nathants/go-hasdefer@latest
install_if_missing govulncheck   golang.org/x/vuln/cmd/govulncheck@latest
install_if_missing modernize     golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest

FAILURES=0
run() {
    echo
    echo ">> $@"
    "$@"
    local rc=$?
    if (( rc != 0 )); then
        FAILURES=1
        echo "<<< command failed (exit ${rc}) : $@" >&2
    fi
}

run go mod tidy

# run govulncheck -show verbose ./...

run go-hasdefer   $(find . -type f -name '*.go')
run exhaustive -default-case-required ./...
run nargs ./...
# run go vet -vettool=$(command -v bodyclose) ./...
run staticcheck ./...
run ineffassign  ./...
run errcheck     ./...
run go vet       ./...
run modernize    ./...


log=$(mktemp)

# Find all packages with test files (excluding integration tests)
test_packages=$(find . -name "*_test.go" -not -path "./integration/*" -not -path "./node_modules/*" -not -path "./.backups/*" | xargs -r dirname | sort -u | sed 's|^\./||')

if [ -n "$test_packages" ]; then
    echo
    echo ">> Testing packages: $test_packages"
    for pkg in $test_packages; do
        echo
        echo ">> Testing $pkg"
        test_binary="/tmp/nina-$(echo $pkg | tr '/' '-').test"
        if [ -f "$test_binary" ]; then
            rm "$test_binary"
        fi
        if go test ./$pkg -race -o "$test_binary" -c 2>&1 | tee -a $log; then
            if [ -f "$test_binary" ]; then
                $test_binary -test.v -test.count=1 2>&1 | tee -a $log
            fi
        fi
    done
    
    if cat $log | grep -e '^WARNING: DATA RACE' -e '^FAIL'; then
        exit 1
    fi
else
    echo ">> No test packages found"
fi

# Build edit binary
echo
echo ">> Building edit binary"
run go build -o edit cmd/edit/main.go

exit "${FAILURES}"
