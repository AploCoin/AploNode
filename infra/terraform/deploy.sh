#!/bin/sh
set -eu
umask 077

module_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
for tool in terraform docker; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Missing $tool. See $module_dir/README.md for prerequisites." >&2
        exit 1
    fi
done
docker info >/dev/null
terraform -chdir="$module_dir" init -input=false
terraform -chdir="$module_dir" validate

# No saved plan is shared between runs and no automatic apply is used.
plan_dir=$(mktemp -d "$module_dir/.terraform/deploy.XXXXXX")
trap 'rm -rf "$plan_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
terraform -chdir="$module_dir" plan -input=false -out="$plan_dir/deploy.tfplan" "$@"
printf '\nApply the plan above? Type yes to continue: '
read -r answer
if [ "$answer" != "yes" ]; then
    echo "Deployment cancelled."
    exit 0
fi
terraform -chdir="$module_dir" apply "$plan_dir/deploy.tfplan"
terraform -chdir="$module_dir" output
echo "For HTTPS, check Certbot/Nginx logs: Terraform completion does not confirm certificate issuance."
