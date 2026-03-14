data "phala_workspace" "current" {}

output "workspace_name" {
  value = data.phala_workspace.current.name
}

output "workspace_tier" {
  value = data.phala_workspace.current.tier
}
