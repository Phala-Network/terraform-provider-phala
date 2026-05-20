resource "phala_app" "web" {
  name      = "web-app"
  size      = "tdx.medium"
  region    = "US-WEST-1"
  image     = "dstack-dev-0.5.7-9b6a5239"
  disk_size = 40

  env = {
    APP_SECRET = "replace-me"
  }

  public_logs     = false
  public_sysinfo  = false
  public_tcbinfo  = false
  gateway_enabled = true

  docker_compose = <<-YAML
    services:
      web:
        image: nginx:stable
        ports:
          - "80:80"
  YAML

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

# The cloud's gateway DNS suffix is exposed as a computed attribute, so
# downstream URLs can be assembled without hardcoding the environment-specific
# domain. Each container port reachable via the gateway is published at
# https://<app_id>-<port>.<gateway_base_domain>.
output "web_url" {
  value = "https://${phala_app.web.app_id}-80.${phala_app.web.gateway_base_domain}"
}
