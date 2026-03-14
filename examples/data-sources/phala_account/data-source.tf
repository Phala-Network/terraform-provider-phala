data "phala_account" "current" {}

output "username" {
  value = data.phala_account.current.username
}

output "workspace_slug" {
  value = data.phala_account.current.workspace_slug
}
