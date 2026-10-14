# AploNode: Terraform + Docker + Nginx

This configuration deploys an isolated Aplo environment on a computer or an
existing Docker server. Terraform builds the repository's images, starts a
discovery bootnode and 1–4 nodes, and configures Nginx in front of node1's RPC and
WebSocket interfaces. A public domain enables Let's Encrypt certificate issuance,
renewal and Nginx reloads.

It does not create a cloud VM, install host packages, change DNS/firewall rules or
migrate an existing blockchain. It uses the repository's built-in native genesis,
including Session Keys. Existing Docker Compose resources are separate and are
not adopted or changed.

## Prerequisites

- Terraform 1.9.8 or newer, below 2.0; the Docker provider is pinned in
  `versions.tf` and `.terraform.lock.hcl`.
- A running Docker Engine with permission to build and run Linux containers.
  On macOS, start Docker Desktop. On a server, install Docker first and run these
  commands from a checkout on that server. Terraform uses the default Docker
  socket or `DOCKER_HOST`; the wrapper also checks `docker info`.
- Internet access to download base images and Go dependencies during the build.
  Size the Docker host and disk for the nodes you choose; archive storage grows
  over time. The first build can take several minutes.

Installation: [Terraform](https://developer.hashicorp.com/terraform/install),
[Docker Engine](https://docs.docker.com/engine/install/).

## One-command local deployment

From the repository root:

```sh
./infra/terraform/deploy.sh
```

The script initializes Terraform, validates the configuration, prints the exact
plan and asks for `yes` before applying that saved plan. It cleans up the temporary
plan and prints the resulting endpoints. Repeat the same command to reconcile
changes; unchanged inputs should not replace containers.

Defaults: `http://localhost:8080` for RPC, `ws://localhost:8080/ws` for WebSocket,
one non-mining node, no TLS/Certbot and no direct node ports on the host. The only
published local port is Nginx on `127.0.0.1:8080`.

Verify JSON-RPC after startup:

```sh
curl -sS http://localhost:8080 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","method":"net_version","params":[],"id":1}'
```

This is an isolated network. Mining is **off by default**, so it does not produce
new blocks until a correctly configured miner is enabled. The scripts do not fund
accounts, allocate mining stake, unlock wallets or manage session signing keys.
Enabling `miner_addresses` only supplies the mining flags; it does not establish
that the address meets Aplo's mining/reward requirements.

Optional settings:

```sh
cp infra/terraform/terraform.tfvars.example infra/terraform/terraform.tfvars
# Edit terraform.tfvars, then run deploy.sh again.
```

For example, set `node_count = 4`. To enable a miner explicitly, put its real
reward address in `miner_addresses`; the first address controls node1, the next
node2, etc. There can be no more addresses than nodes.

## Existing server with automatic HTTPS

Prepare the Docker server and clone this repository there. Create an A record
for the RPC domain pointing to that server. TCP 80 and 443 must be reachable
from the Internet, unused by another service, and permitted by the host/cloud
firewall. If an AAAA record exists, IPv6 must work too; otherwise remove that
record. The module publishes IPv4 ports only, so IPv6 requires separate host
routing. It does not create DNS records or firewall rules.

```sh
cp infra/terraform/server.tfvars.example infra/terraform/terraform.tfvars
```

Edit the copied file:

```hcl
bind_ip           = "0.0.0.0"
http_port         = 80
https_port        = 443
rpc_domain        = "rpc.your-domain.com"
acme_email        = "you@your-domain.com"
rpc_allowed_cidrs = ["YOUR_CLIENT_PUBLIC_IPV4/32"]
acme_staging      = true
```

Replace all placeholders, then run the same `./infra/terraform/deploy.sh` command.
Test with staging first to avoid certificate rate limits. Staging certificates
are intentionally untrusted. After confirming DNS/challenges, set
`acme_staging = false` and rerun; the client replaces the staging certificate with
a production certificate. Do not switch a production deployment back to staging
as a normal operation.

The server endpoints are `https://rpc.your-domain.com` and
`wss://rpc.your-domain.com/ws`. Nginx allows only the configured client IPv4 CIDRs.
The IP list applies to both RPC and WebSocket; ACME challenges remain public.
The client address must be the address seen by Nginx. An extra load balancer or
proxy needs a separately reviewed real-client-IP configuration; arbitrary
`X-Forwarded-For` headers are not trusted.

On a fresh deployment, HTTP serves the ACME challenge and returns 503 for RPC
until a certificate exists. Certbot retries failed issuance every five minutes
and checks for renewal every twelve hours. Nginx checks for changed certificate
files every thirty seconds, validates its new configuration and reloads it.
After issuance, HTTP redirects to HTTPS. Certificate keys are mounted read-only
into Nginx; neither Nginx nor Certbot has access to the Docker socket.

**A successful Terraform apply means the containers were started. It does not
mean DNS validation, certificate issuance, RPC readiness or mining succeeded.**
Check logs and the live endpoint from an allowed client:

```sh
docker logs --tail 100 aplo-tf-certbot
docker logs --tail 100 aplo-tf-nginx
curl -sS https://rpc.your-domain.com \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","method":"net_version","params":[],"id":1}'
```

ACME background behavior follows [Certbot's webroot and renewal workflow](https://eff-certbot.readthedocs.io/en/stable/using.html).

## Resources and configuration

| Resource | Purpose |
| --- | --- |
| Node and tools images | Build `geth` and `bootnode` from this checkout using the existing Dockerfiles. Source changes trigger rebuilds. |
| Nginx image | Build the included HTTP/HTTPS/WebSocket gateway. |
| Internal Docker network | Connect Aplo peers; do not expose their P2P/RPC ports to the host or outside networks. |
| Edge Docker network | Give Nginx/Certbot normal Docker connectivity; nodes stay on the internal network. |
| Bootnode | Peer discovery with the same known **test-only** identity as Compose. It is not a wallet key. |
| Node containers | Use network ID 28282, the built-in native genesis, full sync and archive storage; only `eth,net,web3` over HTTP/WS. |
| Nginx container | Proxy RPC `/` to node1:8545 and WebSocket `/ws` to node1:8546. |
| Certbot container (domain mode) | Issue and renew one domain's certificate using HTTP-01. |
| Data volumes | One blockchain volume per node; plus certificates, challenge files and ACME working state with HTTPS. |

`variables.tf` lists all settings. `name` defaults to `aplo-tf` to avoid collisions
with Compose. The node subnet defaults to `172.29.82.0/24`; change `node_subnet`
before deployment if it overlaps another Docker/VPN network. Fixed internal
addresses keep the bootnode and Nginx upstream stable across container replacement.
This single-host layout does not connect multiple servers into one public network
and is not a production high-availability/backup design.

## Inspection, updates and data

Inspect the infrastructure before changing anything:

```sh
terraform -chdir=infra/terraform init
terraform -chdir=infra/terraform plan
terraform -chdir=infra/terraform show
terraform -chdir=infra/terraform output
docker logs --tail 100 aplo-tf-node1
```

Images can be rebuilt and containers replaced while reusing their named volumes.
Blockchain and certificate volumes have `prevent_destroy = true`. Consequently,
`terraform destroy`, reducing `node_count`, disabling an established TLS deployment
or changing its resource prefix is blocked if it would delete protected volumes.
Back up the data and explicitly review any such removal; this module provides no
automatic data-deletion switch. Docker commands can still delete volumes outside
Terraform, so this is not a backup mechanism.

Do not reuse an old database whose genesis lacks native Session Keys/GAplo state;
the current client rejects it. This configuration does not delete or migrate old
data. Changing `--networkid` alone would not change the genesis or signing domain.

Keep the `.terraform.lock.hcl` in Git. Local state, plans and `terraform.tfvars`
are ignored by Git and excluded from the Docker build context. State can contain
deployment details; protect and back it up on the target host. Use one state per
deployment and do not copy the same live state to another Docker host. No remote
state backend is configured in this initial setup.

## Validation without deploying

```sh
terraform -chdir=infra/terraform init -backend=false
terraform -chdir=infra/terraform fmt -check -recursive
terraform -chdir=infra/terraform validate
terraform -chdir=infra/terraform test
for script in infra/terraform/deploy.sh infra/terraform/certbot.sh infra/terraform/gateway/entrypoint.sh infra/terraform/tests/gateway-smoke.sh; do
  sh -n "$script"
done
```

The Terraform tests use a mocked Docker provider and `command = plan` throughout.
They check local defaults, HTTPS configuration, RPC isolation, IP restrictions,
mining opt-in and invalid inputs **without building images or creating resources**.
They do not establish Docker runtime, blockchain or live Let's Encrypt acceptance.

With a running Docker daemon, `sh infra/terraform/tests/gateway-smoke.sh` also tests
real Nginx locally using a dummy backend and self-signed certificates: RPC/WS path
routing, HTTP-01 bootstrap, HTTPS activation, certificate reload and denied client
IPs. It uses temporary containers, loopback ports and files, then cleans them up.
CI runs these checks; they still do not exercise live ACME or an Aplo blockchain.
