variables {
  source_commit = "1111111111111111111111111111111111111111"
  deployment = {
    deployment_mode  = "node"
    bootstrap_enodes = ["enode://79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8@198.51.100.2:30303"]
  }
}

run "render_user_data" {
  command = plan
  assert {
    condition     = startswith(output.user_data, "#cloud-config") && strcontains(output.user_data, "encoding: b64") && strcontains(output.user_data, var.source_commit)
    error_message = "Render cloud-init with embedded installer/config and an immutable source revision."
  }
  assert {
    condition     = strcontains(output.user_data, base64encode(jsonencode(var.deployment))) && strcontains(output.user_data, filebase64("${path.module}/../terraform/bootstrap.sh"))
    error_message = "Cloud-init must contain exactly the reviewed bootstrap script and JSON deployment config."
  }
}

run "reject_mutable_source" {
  command = plan
  variables { source_commit = "master" }
  expect_failures = [var.source_commit]
}

run "reject_missing_live_seeds" {
  command = plan
  variables { deployment = { deployment_mode = "node" } }
  expect_failures = [var.deployment]
}

run "render_bootnode" {
  command = plan
  variables { deployment = { deployment_mode = "bootnode", advertise_ip = "203.0.113.20" } }
  assert {
    condition     = strcontains(output.user_data, base64encode(jsonencode(var.deployment)))
    error_message = "Bootnode profile must render without requiring an already running network."
  }
}
