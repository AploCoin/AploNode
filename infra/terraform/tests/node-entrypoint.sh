#!/bin/sh
# Exercise startup decisions without geth or a network connection.
set -eu
module_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cat > "$work/geth" <<'GETH'
#!/bin/sh
printf '%s\n' "$@" > "$APLO_TEST_ARGS"
GETH
chmod +x "$work/geth"
export PATH="$work:$PATH" APLO_TEST_ARGS="$work/args" BOOTNODE_HOST=
if BOOTSTRAP_ENODES= sh "$module_dir/node-entrypoint.sh" 2>"$work/error"; then
    echo "Unseeded node was incorrectly allowed to start." >&2
    exit 1
fi
test ! -e "$work/args"
grep -q 'No bootstrap enodes configured' "$work/error"
export BOOTSTRAP_ENODES="enode://test-public-key@198.51.100.2:30303"
sh "$module_dir/node-entrypoint.sh" --networkid=28282 --syncmode=full
# Exact argument boundaries prevent shell expansion/evaluation of the seed value.
test "$(sed -n '1p' "$work/args")" = "--bootnodes=$BOOTSTRAP_ENODES"
test "$(sed -n '2p' "$work/args")" = --networkid=28282
export BOOTSTRAP_ENODES='$(touch /tmp/aplo-should-never-execute)'
sh "$module_dir/node-entrypoint.sh" --syncmode=full
test "$(sed -n '1p' "$work/args")" = "--bootnodes=$BOOTSTRAP_ENODES"
echo 'PASS: unseeded startup denied; external seeds passed as literal geth arguments.'
