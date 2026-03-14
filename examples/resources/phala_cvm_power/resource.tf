resource "phala_app" "web" {
  name      = "power-demo"
  size      = "tdx.medium"
  region    = "US-WEST-1"
  image     = "dstack-dev-0.5.7-9b6a5239"
  disk_size = 40
  replicas  = 1

  docker_compose = <<-YAML
    services:
      web:
        image: nginx:stable
        ports:
          - "80:80"
  YAML
}

resource "phala_cvm_power" "web" {
  cvm_id = phala_app.web.primary_cvm_id
  state  = "stopped"

  wait_for_state       = true
  wait_timeout_seconds = 900
}
