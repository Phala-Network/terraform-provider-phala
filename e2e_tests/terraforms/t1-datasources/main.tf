terraform {
  required_providers {
    phala = {
      source = "phala-network/phala"
    }
  }
}

provider "phala" {}

# 1. Workspace identity — verifies the API key resolves and the workspace
#    endpoints (/auth/me, /workspaces/current) are wired through the SDK.
data "phala_workspace" "current" {}

# 2. Account/usage info — exercises a separate billing endpoint.
data "phala_account" "current" {}

# 3. Catalog data sources — these hit GET endpoints with no parameters.
data "phala_regions" "all" {}

data "phala_sizes" "all" {}

data "phala_images" "all" {}

# 4. Nodes list — same pattern, exercises pagination if any.
data "phala_nodes" "all" {}

output "workspace_id" {
  value = data.phala_workspace.current.id
}

output "regions_count" {
  value = length(data.phala_regions.all.regions)
}

output "sizes_count" {
  value = length(data.phala_sizes.all.sizes)
}

output "images_count" {
  value = length(data.phala_images.all.images)
}

output "nodes_count" {
  value = length(data.phala_nodes.all.nodes)
}

output "account_summary" {
  value = data.phala_account.current
}
