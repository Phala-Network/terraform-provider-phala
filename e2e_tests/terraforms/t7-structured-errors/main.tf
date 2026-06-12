terraform {
  required_providers {
    phala = {
      source = "phala-network/phala"
    }
  }
}

provider "phala" {}

# This config intentionally fails: docker_compose is invalid YAML so the
# preflight endpoint returns a structured error (with error_code, hint, etc).
# We're validating that the provider surfaces the structured fields via
# APIError.IsStructured() / FormatError() — NOT just the raw HTTP body.

data "phala_app_preflight" "bad_compose" {
  name   = "tf-bad-compose"
  region = "us-west-1"
  size   = "tdx.small"
  image  = "dstack-0.5.3"

  docker_compose = "this is not yaml: : : ::: invalid"
}

output "should_never_reach_here" {
  value = data.phala_app_preflight.bad_compose.compose_hash
}
