#!/bin/sh
# Real Nginx checks with a dummy upstream and self-signed certificates.
# No blockchain, real ACME account or public port is used.
set -eu
module_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_id="aplo-gateway-test-$$"
work=$(mktemp -d)
cleanup() {
    docker rm -f "$test_id-local" "$test_id-tls" "$test_id-backend" >/dev/null 2>&1 || :
    docker network rm "$test_id" >/dev/null 2>&1 || :
    docker image rm "$test_id" >/dev/null 2>&1 || :
    rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker network create "$test_id" >/dev/null
docker build -t "$test_id" "$module_dir/gateway"
cat > "$work/backend.conf" <<'EOF'
server {
    listen 8545;
    location / { return 200 'rpc-upstream'; }
}
server {
    listen 8546;
    location / { return 200 'ws-upstream'; }
}
EOF
docker run -d --name "$test_id-backend" --network "$test_id" --network-alias node1 \
    -v "$work/backend.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.28.0-alpine >/dev/null
docker run -d --name "$test_id-local" --network "$test_id" \
    -p 127.0.0.1::80 -e TLS_ENABLED=false -e RPC_DOMAIN=localhost -e RPC_ACL= \
    "$test_id" >/dev/null
local_port=$(docker port "$test_id-local" 80/tcp | sed 's/.*://')
for attempt in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:$local_port" > "$work/response" 2>/dev/null; then break; fi
    sleep 1
done
test "$(cat "$work/response")" = rpc-upstream
test "$(curl -fsS "http://127.0.0.1:$local_port/ws")" = ws-upstream

mkdir -p "$work/certs/live/rpc.example.com" "$work/acme/.well-known/acme-challenge"
echo challenge-ok > "$work/acme/.well-known/acme-challenge/test"
docker run -d --name "$test_id-tls" --network "$test_id" \
    -p 127.0.0.1::80 -p 127.0.0.1::443 \
    -v "$work/certs:/etc/letsencrypt:ro" -v "$work/acme:/var/www/acme:ro" \
    -e TLS_ENABLED=true -e RPC_DOMAIN=rpc.example.com -e RPC_ACL= \
    "$test_id" >/dev/null
http_port=$(docker port "$test_id-tls" 80/tcp | sed 's/.*://')
tls_port=$(docker port "$test_id-tls" 443/tcp | sed 's/.*://')
for attempt in $(seq 1 30); do
    status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$http_port" 2>/dev/null || :)
    if [ "$status" = 503 ]; then break; fi
    sleep 1
done
test "$status" = 503
test "$(curl -fsS "http://127.0.0.1:$http_port/.well-known/acme-challenge/test")" = challenge-ok
issue_test_cert() {
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=rpc.example.com \
        -keyout "$work/certs/live/rpc.example.com/privkey.pem" \
        -out "$work/certs/live/rpc.example.com/fullchain.pem" >/dev/null 2>&1
}
issue_test_cert
for attempt in $(seq 1 45); do
    if curl -kfsS --resolve "rpc.example.com:$tls_port:127.0.0.1" \
        "https://rpc.example.com:$tls_port" > "$work/response" 2>/dev/null; then break; fi
    sleep 1
done
test "$(cat "$work/response")" = rpc-upstream
test "$(curl -kfsS --resolve "rpc.example.com:$tls_port:127.0.0.1" "https://rpc.example.com:$tls_port/ws")" = ws-upstream
test "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$http_port")" = 301
test "$(curl -fsS "http://127.0.0.1:$http_port/.well-known/acme-challenge/test")" = challenge-ok

# Replacing cert files should reload Nginx without restarting its container.
issue_test_cert
expected=$(openssl x509 -in "$work/certs/live/rpc.example.com/fullchain.pem" -noout -serial)
for attempt in $(seq 1 45); do
    actual=$(openssl s_client -connect "127.0.0.1:$tls_port" -servername rpc.example.com </dev/null 2>/dev/null | openssl x509 -noout -serial)
    if [ "$actual" = "$expected" ]; then break; fi
    sleep 1
done
test "$actual" = "$expected"
docker exec "$test_id-tls" nginx -t

# An unrelated allowlisted client must not permit RPC or /ws, while ACME works.
docker rm -f "$test_id-tls" >/dev/null
acl=$(printf 'allow 203.0.113.10/32;\ndeny all;')
docker run -d --name "$test_id-tls" --network "$test_id" \
    -p 127.0.0.1::80 -p 127.0.0.1::443 \
    -v "$work/certs:/etc/letsencrypt:ro" -v "$work/acme:/var/www/acme:ro" \
    -e TLS_ENABLED=true -e RPC_DOMAIN=rpc.example.com -e "RPC_ACL=$acl" \
    "$test_id" >/dev/null
http_port=$(docker port "$test_id-tls" 80/tcp | sed 's/.*://')
tls_port=$(docker port "$test_id-tls" 443/tcp | sed 's/.*://')
for attempt in $(seq 1 45); do
    status=$(curl -ksS -o /dev/null -w '%{http_code}' --resolve "rpc.example.com:$tls_port:127.0.0.1" "https://rpc.example.com:$tls_port" 2>/dev/null || :)
    if [ "$status" = 403 ]; then break; fi
    sleep 1
done
test "$status" = 403
test "$(curl -ksS -o /dev/null -w '%{http_code}' --resolve "rpc.example.com:$tls_port:127.0.0.1" "https://rpc.example.com:$tls_port/ws")" = 403
test "$(curl -fsS "http://127.0.0.1:$http_port/.well-known/acme-challenge/test")" = challenge-ok
echo 'PASS: local RPC/WS routing, fail-closed bootstrap, HTTPS activation, certificate reload and ACL.'
