# AploNode deployment: Docker, Terraform, Nginx and HTTPS

Deploy on an existing Docker host, prepare an empty Ubuntu server, or give the
[cloud-init module](../cloud-init/README.md) to your VPS provider.

## Choose the node profile

| `deployment_mode` | Starts | Blockchain connection |
| --- | --- | --- |
| `node` **(default)** | Ordinary full node + Nginx | Syncs the existing Aplo blockchain through `bootstrap_enodes`. No mining and no local bootnode. |
| `miner` | Full node with mining enabled + Nginx | Syncs the same existing blockchain. Requires one `miner_addresses` reward address per node. |
| `bootnode` | Discovery bootnode + ordinary node + Nginx | Provides discovery to other servers. Add existing-network seeds to join that network; empty seeds explicitly start a first-network environment. |

`node_count` defaults to 1 (maximum 4). The ordinary/miner profiles **refuse an
unseeded start**: a successfully started isolated chain is not treated as live
network synchronization. A local bootnode is created only in `bootnode` mode.

`http://pub1.aplocoin.com/` is the operator-provided future network reference.
It was offline when this configuration was prepared; no public enode or live
network acceptance has been established. An HTTP URL is an RPC/web address, not
an enode. Once the node is online, get its public P2P identity via
`admin.nodeInfo.enode` on its local geth console, verify the advertised IP/port,
and put the resulting `enode://<128-hex-public-key>@host:30303` in the seed list.
There is no invented or hardcoded public network identity in these examples.

## Existing Docker host: one command

Prerequisites: Terraform >= 1.9.8 and < 2.0, a running Linux-container Docker
Engine with Buildx and Internet access for image/Go dependency downloads. On macOS start
Docker Desktop. No native Go installation is required.

From the repository root:

```sh
cp infra/terraform/terraform.tfvars.example infra/terraform/terraform.tfvars
# Fill bootstrap_enodes with the live network's P2P addresses, then:
./infra/terraform/deploy.sh
```

The command initializes Terraform, validates settings, prints the plan and asks
for `yes` before applying the saved plan. Repeat to reconcile changes. For
unattended automation, `./infra/terraform/deploy.sh --yes` explicitly applies the
printed plan without a prompt. Do not pass `--yes` until the configuration has
been reviewed.

Default RPC: `http://localhost:8080`; WebSocket: `ws://localhost:8080/ws`.
Nginx binds loopback. Direct node RPC/WS ports are never published. HTTP/WS expose
only `eth,net,web3`; accounts are not unlocked.

### Mining node

```hcl
deployment_mode = "miner"
bootstrap_enodes = ["enode://<REAL_PUBLIC_KEY>@pub1.aplocoin.com:30303"]
miner_addresses = ["0xYOUR_40_HEX_CHARACTER_REWARD_ADDRESS"]
```

Replace placeholders. This enables mining flags and normal synchronization; it
does not fund accounts, acquire staking/reward eligibility or guarantee block
production. The existing client's sync and mining rules still apply.

### Bootnode + node

Copy `bootnode.tfvars.example`, set this server's real `advertise_ip`, and add
existing-network seeds if applicable. The bootnode generates a random identity
once and preserves it in a protected volume. Nodes see only its public ID through
a separate read-only discovery volume. No shared test private key is used.

After deployment, `terraform -chdir=infra/terraform output -raw bootnode_enode_command`
prints a command you can run on the Docker host to obtain its public enode. Share
that enode with other nodes, not the contents of the identity volume.

## Empty server: install and deploy in one command

Supported target: **Ubuntu 22.04 or 24.04 with systemd**, amd64/arm64, root access
and outbound Internet. Run this on the target server, not on your Mac. This path
installs packages and starts Docker; it does not rent a server or manage DNS.

Download `bootstrap.sh` from the reviewed release/commit. Prepare a JSON settings
file using `bootstrap.tfvars.json.example`, filling `bootstrap_enodes` (or choosing
`bootnode` plus `advertise_ip`). Then run:

```sh
sudo sh bootstrap.sh /root/aplo.tfvars.json FULL_40_CHARACTER_COMMIT_SHA
```

The installer:

1. Checks the supported OS, root privileges, source revision and configuration.
2. Installs Docker from its official Ubuntu repository, and Terraform 1.9.8 if
   absent (download checked against HashiCorp's SHA-256 sums).
3. Checks out that exact source commit in `/opt/aplonode`, refusing to overwrite
   tracked local edits or a checkout of another repository.
4. Writes the deployment settings and runs `deploy.sh --yes`, building AploNode,
   creating containers/volumes/networks and configuring Nginx/optional Certbot.

Use a commit containing this deployment code. The optional third argument is a
GitHub HTTPS repository URL; it is useful for reviewing a fork's commit before
merge. Existing supported Terraform is reused; an incompatible version fails the
module's version constraint. No SSH password/private key is needed in Terraform.
The caller already has server access; SSH login and VM allocation are separate.

For a custom genesis in unattended bootstrap, include a `genesis_json` object in
the JSON settings. The installer writes it on the target and converts it into
`genesis_path`. A source-computer path is not automatically copied to the server.

For a VPS created through a third-party provider, supply the rendered
[cloud-init user data](../cloud-init/README.md) instead; the same installer runs
on first boot. No paid provider or VM is selected/created by this repository.

## Network identity, synchronization and ports

`network_id` defaults to 28282. The network ID alone does not establish chain
identity: genesis, chain ID, consensus rules and client revision must match the
live network. The default uses this source revision's built-in genesis. If the
live network has its own genesis, set `genesis_path` to that exact JSON file.
Startup runs `geth init` against the persisted database; it never deletes data or
migrates an incompatible chain. A custom genesis does not repair a client whose
protocol rules differ from the live network.

The current client requires native Session Keys/GAplo genesis state. This is not
a migration for a legacy running network. Verify the deployed source and genesis
against the actual network before claiming live-chain synchronization.

Nodes have outbound Internet/P2P access on a Docker bridge. Host TCP/UDP 30303
is published for node1; subsequent nodes increment the port. The optional
bootnode publishes UDP 30301. Allow these ports in provider/host firewalls and set
`advertise_ip` when publishing an address behind NAT. Without `advertise_ip`, an
ordinary node can still initiate outgoing sync connections. RPC is controlled
separately by Nginx; enabling P2P does not expose RPC.

`node_subnet` defaults to `172.29.82.0/24`. Change it before deployment if it
overlaps Docker/VPN networks. Full synchronization with pruning (`gcmode=full`)
is used. Plan sufficient storage for the live chain. This is a single-host layout,
not a backup or high-availability design.

## Nginx and automatic HTTPS

Copy `server.tfvars.example`, keep the selected profile/seeds, then configure:

```hcl
bind_ip           = "0.0.0.0"
http_port         = 80
https_port        = 443
rpc_domain        = "rpc.your-domain.com"
acme_email        = "you@your-domain.com"
rpc_allowed_cidrs = ["YOUR_CLIENT_PUBLIC_IPV4/32"]
acme_staging      = true
```

A/AAAA records must point to the server and TCP 80/443 must be reachable and
unused. This module binds IPv4; remove unusable AAAA records or arrange IPv6
routing separately. DNS and host/provider firewall rules are not changed.

Before first issuance, HTTP serves ACME challenges and returns 503 for RPC.
After issuance, HTTP redirects to HTTPS. Certbot retries failures every five
minutes, checks renewal every twelve hours, and Nginx reloads changed certificates
within thirty seconds. Staging certificates are untrusted; after testing, set
`acme_staging = false` and rerun to replace them with trusted production
certificates. Certificate/ACME state persists across container replacements.

Allowed client CIDRs apply to RPC and `/ws`, not ACME challenges. An additional
load balancer needs its own reviewed real-client-IP configuration. Nginx does not
trust arbitrary X-Forwarded-For headers. No Docker socket is mounted in Nginx or
Certbot.

## Inspect the running result

```sh
terraform -chdir=infra/terraform plan
terraform -chdir=infra/terraform show
terraform -chdir=infra/terraform output
docker logs --tail 100 aplo-tf-node1
docker logs --tail 100 aplo-tf-nginx
# HTTPS profile only:
docker logs --tail 100 aplo-tf-certbot
```

Check actual peers and sync progress (use your HTTPS endpoint for a server):

```sh
curl -sS http://localhost:8080 -H 'Content-Type: application/json' \
  --data '[{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":1},{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":2},{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":3},{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x0",false],"id":4}]'
```

Compare chain ID/genesis hash with a trusted live node, check peer count and
advancing head height. `eth_syncing=false` alone does not prove successful sync:
an isolated node can also return false. Terraform apply completion proves only
resource creation, not live-chain sync, mining readiness or certificate issuance.

## Data protection and upgrades

One named volume per node stores chain data. Blockchain, bootnode private identity
and TLS/ACME volumes use `prevent_destroy=true`. Full destruction, reducing node
count, removing an established bootnode/TLS profile or changing names is blocked
if it would delete a protected volume. Back up and explicitly review any such
transition. Docker can still delete volumes outside Terraform; protection is not
a backup.

The old, unreleased isolated-network draft is not silently migrated to the new
profiles. Operators who applied that draft must review networking/identity changes
and preserve their existing data. Existing Compose resources are not adopted.

Keep `.terraform.lock.hcl` in Git. Local state, plans and tfvars are ignored by Git
and excluded from build contexts. The host's state must be protected/backed up;
use one state per deployment. No shared remote-state backend is configured.

## Checks without deploying to a server

```sh
terraform -chdir=infra/terraform init -backend=false
terraform -chdir=infra/terraform fmt -check -recursive
terraform -chdir=infra/terraform validate
terraform -chdir=infra/terraform test
terraform -chdir=infra/cloud-init init -backend=false
terraform -chdir=infra/cloud-init test
sh infra/terraform/tests/node-entrypoint.sh
```

Mock-provider tests check all three profiles and reject missing seeds, missing
mining addresses, unsafe RPC publication and invalid TLS settings. Cloud-init
tests check immutable revision/config rendering without any provider or VM.
With Terraform and Docker, `sh infra/terraform/tests/gateway-smoke.sh` checks the
Docker provider's Buildx image build and real Nginx using
dummy upstreams and self-signed certificates. CI runs these tests. None establishes
first-boot installation on a real VPS or synchronization with the offline network.
