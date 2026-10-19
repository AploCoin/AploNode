#!/bin/sh
# Run as root ON a fresh Ubuntu 22.04/24.04 server. Never run on the operator's Mac.
# Usage: sudo sh bootstrap.sh CONFIG.tfvars.json SOURCE_COMMIT [REPOSITORY_URL]
set -eu
umask 077
fail() { echo "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail "Run bootstrap as root on the target server."
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || fail "Usage: bootstrap.sh CONFIG.tfvars.json SOURCE_COMMIT [REPOSITORY_URL]"
config=$1
source_ref=$2
repository=${3:-https://github.com/AploCoin/AploNode.git}
[ -f "$config" ] || fail "Missing configuration JSON: $config"
case "$source_ref" in *[!0-9a-f]*|'') fail "SOURCE_COMMIT must be a full 40-character commit SHA.";; esac
[ "${#source_ref}" = 40 ] || fail "SOURCE_COMMIT must be a full 40-character commit SHA."
case "$repository" in https://github.com/*) ;; *) fail "Use an HTTPS GitHub repository URL.";; esac
# Supported host contract is deliberately narrow and checked before package changes.
. /etc/os-release
[ "$ID" = ubuntu ] || fail "Supported hosts: Ubuntu 22.04/24.04 with systemd."
case "$VERSION_ID" in 22.04|24.04) ;; *) fail "Supported hosts: Ubuntu 22.04/24.04 with systemd.";; esac
[ -d /run/systemd/system ] || fail "A running systemd host is required (not an ordinary Docker container)."
[ -z "${DOCKER_HOST:-}" ] || fail "Unset DOCKER_HOST: bootstrap configures Docker on this host."
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl git unzip python3
python3 - "$config" <<'CHECK'
import json, sys
with open(sys.argv[1]) as f:
    config = json.load(f)
if not isinstance(config, dict):
    raise SystemExit("Configuration must be a JSON object.")
mode = config.get("deployment_mode", "node")
if mode not in ("node", "miner", "bootnode"):
    raise SystemExit("deployment_mode must be node, miner or bootnode.")
if mode != "bootnode" and not config.get("bootstrap_enodes"):
    raise SystemExit("The default node/miner needs live bootstrap_enodes. HTTP is not P2P.")
if mode == "bootnode" and not config.get("advertise_ip"):
    raise SystemExit("bootnode requires advertise_ip.")
if config.get("genesis_path"):
    raise SystemExit("For unattended bootstrap, use genesis_json; a path from another computer is not copied.")
CHECK
if ! command -v docker >/dev/null 2>&1; then
    install -d -m 0755 /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    arch=$(dpkg --print-architecture)
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu %s stable\n' "$arch" "$VERSION_CODENAME" > /etc/apt/sources.list.d/aplonode-docker.list
    apt-get update
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
docker buildx version >/dev/null 2>&1 || fail "Docker exists but Buildx is missing. Install docker-buildx-plugin, then rerun."
systemctl enable --now docker
docker info >/dev/null
if ! command -v terraform >/dev/null 2>&1; then
    case "$(uname -m)" in x86_64) tf_arch=amd64;; aarch64|arm64) tf_arch=arm64;; *) fail "Unsupported CPU architecture.";; esac
    tf_version=1.9.8
    download=$(mktemp -d)
    trap 'rm -rf "$download"' EXIT
    archive="terraform_${tf_version}_linux_${tf_arch}.zip"
    curl -fsSL "https://releases.hashicorp.com/terraform/$tf_version/$archive" -o "$download/$archive"
    curl -fsSL "https://releases.hashicorp.com/terraform/$tf_version/terraform_${tf_version}_SHA256SUMS" -o "$download/SHA256SUMS"
    (cd "$download"; grep "  $archive\$" SHA256SUMS | sha256sum -c -; unzip -o "$archive")
    install -m 0755 "$download/terraform" /usr/local/bin/terraform
    rm -rf "$download"
    trap - EXIT
fi
install_dir=/opt/aplonode
if [ -e "$install_dir" ] && [ ! -d "$install_dir/.git" ]; then
    fail "$install_dir already exists and is not an AploNode checkout."
fi
if [ ! -d "$install_dir/.git" ]; then
    install -d -m 0700 "$install_dir"
    git -C "$install_dir" init
    git -C "$install_dir" remote add origin "$repository"
fi
[ "$(git -C "$install_dir" remote get-url origin)" = "$repository" ] || fail "Existing checkout uses a different repository."
[ -z "$(git -C "$install_dir" status --porcelain --untracked-files=no)" ] || fail "Checkout has local edits; refusing to overwrite them."
git -C "$install_dir" fetch --depth=1 origin "$source_ref"
[ "$(git -C "$install_dir" rev-parse FETCH_HEAD)" = "$source_ref" ] || fail "Fetched revision does not match SOURCE_COMMIT."
git -C "$install_dir" checkout --detach FETCH_HEAD
# Install config atomically; never evaluate configuration as shell code.
python3 - "$config" "$install_dir/infra/terraform" <<'CONFIG'
import json, os, pathlib, sys
with open(sys.argv[1]) as f:
    config = json.load(f)
module = pathlib.Path(sys.argv[2])
genesis = config.pop("genesis_json", None)
if genesis is not None:
    path = module / "bootstrap-genesis.json"
    path.write_text(json.dumps(genesis))
    path.chmod(0o600)
    config["genesis_path"] = str(path)
tmp = module / "terraform.tfvars.json.tmp"
tmp.write_text(json.dumps(config, indent=2) + "\n")
tmp.chmod(0o600)
os.replace(tmp, module / "terraform.tfvars.json")
CONFIG
"$install_dir/infra/terraform/deploy.sh" --yes
echo "Host configuration applied. Check peer count, chain ID/genesis, sync progress and certificate logs before use."
