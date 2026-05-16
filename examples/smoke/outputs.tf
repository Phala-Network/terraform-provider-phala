output "sizes_count" {
  value = length(data.phala_sizes.all.sizes)
}

output "regions_count" {
  value = length(data.phala_regions.all.regions)
}

output "images_count" {
  value = length(data.phala_images.all.images)
}

output "account_username" {
  value = data.phala_account.current.username
}

output "workspace_slug" {
  value = data.phala_workspace.current.slug
}

output "app_id" {
  value = var.create_resources ? phala_app.smoke[0].app_id : null
}

output "app_endpoint" {
  value = var.create_resources ? phala_app.smoke[0].endpoint : null
}

output "app_cvm_ids" {
  value = var.create_resources ? phala_app.smoke[0].cvm_ids : null
}

output "app_instances" {
  value = var.create_resources ? phala_app.smoke[0].instances : null
}

output "app_instance_vm_uuids" {
  value = var.create_resources ? [for instance in phala_app.smoke[0].instances : instance.vm_uuid] : null
}

output "app_instance_ids" {
  value = var.create_resources ? [for instance in phala_app.smoke[0].instances : instance.instance_id] : null
}

output "consumer_app_id" {
  value = var.create_resources && var.create_consumer_app ? phala_app.consumer[0].app_id : null
}

output "ssh_key_id" {
  value = var.create_resources && var.ssh_public_key != "" ? phala_ssh_key.smoke[0].id : null
}

output "cvm_power_status" {
  value = var.create_resources && var.cvm_power_state != "" ? phala_cvm_power.smoke[0].status : null
}
