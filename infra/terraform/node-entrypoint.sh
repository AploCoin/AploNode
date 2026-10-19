#!/bin/sh
set -eu
bootnodes=${BOOTSTRAP_ENODES:-}
if [ -n "${BOOTNODE_HOST:-}" ]; then
    attempts=0
    until [ -s /bootstrap/node.id ]; do
        attempts=$((attempts + 1))
        if [ "$attempts" -ge 30 ]; then
            echo "Local bootnode identity is unavailable; refusing an unseeded start." >&2
            exit 1
        fi
        sleep 1
    done
    local_enode="enode://$(cat /bootstrap/node.id)@$BOOTNODE_HOST:30301"
    bootnodes="${bootnodes:+$bootnodes,}$local_enode"
fi
if [ -z "$bootnodes" ]; then
    echo "No bootstrap enodes configured; refusing an unseeded node/miner." >&2
    exit 1
fi
# geth init checks the persisted genesis; never delete/migrate an existing DB.
if [ -s /etc/aplo/genesis.json ]; then
    geth --datadir=/root/.ethereum init /etc/aplo/genesis.json
fi
exec geth "--bootnodes=$bootnodes" "$@"
