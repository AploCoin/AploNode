#!/bin/sh
set -eu
trap 'exit 0' TERM INT
while :; do
    set -- certonly --non-interactive --agree-tos --webroot \
        --webroot-path /var/www/acme --cert-name "$RPC_DOMAIN" \
        --domain "$RPC_DOMAIN" --email "$ACME_EMAIL"
    if [ "$ACME_STAGING" = "true" ]; then
        set -- "$@" --staging
    else
        # Switching to production must replace a still-valid staging certificate.
        set -- "$@" --server https://acme-v02.api.letsencrypt.org/directory
    fi
    renewal="/etc/letsencrypt/renewal/$RPC_DOMAIN.conf"
    if [ "$ACME_STAGING" != "true" ] && [ -f "$renewal" ] && \
        grep -q '^server = https://acme-staging-v02.api.letsencrypt.org/' "$renewal"; then
        set -- "$@" --force-renewal
    else
        set -- "$@" --keep-until-expiring
    fi
    if certbot "$@"; then
        delay=43200
    else
        echo "Certificate issuance/renewal failed; retrying in five minutes." >&2
        delay=300
    fi
    sleep "$delay" &
    wait $! || :
done
