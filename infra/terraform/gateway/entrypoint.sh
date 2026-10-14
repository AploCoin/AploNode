#!/bin/sh
set -eu

render() {
    envsubst '${RPC_DOMAIN} ${RPC_ACL}' < "/opt/aplo/$1.template" > /etc/nginx/conf.d/default.conf
}

if [ "$TLS_ENABLED" = "true" ]; then
    render bootstrap
    (
        previous=""
        while sleep 30; do
            cert="/etc/letsencrypt/live/$RPC_DOMAIN/fullchain.pem"
            key="/etc/letsencrypt/live/$RPC_DOMAIN/privkey.pem"
            if [ -s "$cert" ] && [ -s "$key" ]; then
                current=$(sha256sum "$cert" "$key")
                if [ "$current" != "$previous" ]; then
                    cp /etc/nginx/conf.d/default.conf /etc/nginx/conf.d/default.backup
                    render https
                    if nginx -t && nginx -s reload; then
                        previous=$current
                    else
                        mv /etc/nginx/conf.d/default.backup /etc/nginx/conf.d/default.conf
                    fi
                    rm -f /etc/nginx/conf.d/default.backup
                fi
            fi
        done
    ) &
else
    render local
fi
exec nginx -g 'daemon off;'
