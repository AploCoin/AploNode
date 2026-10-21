# First-boot deployment through a VPS provider

This Terraform module renders **cloud-init user data**, containing the same
[empty-server installer](../terraform/bootstrap.sh) and JSON settings used for
manual server setup. It creates no VM and calls no paid provider APIs.

Target images: Ubuntu 22.04/24.04 with systemd and cloud-init, amd64/arm64.
Choose your VM/provider/size/SSH key separately. The provider must pass user data
to cloud-init and permit outgoing package/image downloads. Set its firewall rules
for P2P, and HTTP/HTTPS if those are enabled.

Use it inside the Terraform configuration that creates your VPS:

```hcl
module "aplo_first_boot" {
  source        = "/path/to/AploNode/infra/cloud-init"
  source_commit = "FULL_40_CHARACTER_REVIEWED_COMMIT_SHA"
  deployment = {
    deployment_mode = "node" # default profile
    bootstrap_enodes = ["enode://<REAL_PUBLIC_KEY>@pub1.aplocoin.com:30303"]
    # Optional for HTTPS: rpc_domain, acme_email, bind_ip, ports, IP allowlist.
    # Optional custom chain: genesis_json = jsondecode(file("genesis.json"))
  }
}

# In your chosen provider's VM resource:
# user_data = module.aplo_first_boot.user_data
```

Replace placeholders. `node` and `miner` require real P2P enodes; HTTP URLs are not
accepted. For `miner`, add `miner_addresses`; for `bootnode`, set `advertise_ip`.
The source commit must contain the deployment scripts. Before merge, set
`repository_url` to the reviewed fork if that commit exists only there.

You can also render user data standalone by copying `terraform.tfvars.example`,
filling it and running this **provider-free** module:

```sh
terraform -chdir=infra/cloud-init init
terraform -chdir=infra/cloud-init apply
terraform -chdir=infra/cloud-init output -raw user_data > /tmp/aplonode-cloud-init.yaml
```

Paste that YAML into the VPS provider's cloud-init/user-data field at VM creation.
The module apply above only records outputs locally; creating a VM requires your
separate provider resource. Cloud-init runs the installer without an interactive
prompt, so review the image, source commit and deployment settings first.
Do not place private keys/passwords in user data or the settings object: providers
and Terraform state may retain it.

On the new server, inspect `cloud-init status --long`,
`/var/log/cloud-init-output.log`, `/opt/aplonode/infra/terraform/terraform.tfstate`
and `docker logs aplo-tf-node1`. First boot may take several minutes. `runcmd`
runs once; changing user data is not a general update mechanism. For updates,
rerun the installer on the same server with a reviewed commit, or use the checked
out `deploy.sh` with its saved configuration. Chain/identity volumes are retained.

No real VPS or public-chain synchronization is implied by rendering this file.
See the deployment guide for genesis/protocol compatibility and live sync checks.

Cloud-init background: [HashiCorp's provisioning guide](https://developer.hashicorp.com/terraform/tutorials/provision/cloud-init).
