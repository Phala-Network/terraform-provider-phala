SHELL := /bin/bash

# Resolve the provider repo root (one level up from e2e_tests/).
PROVIDER_DIR     := $(abspath $(CURDIR)/..)

# Mirror the dev-build paths used by the provider's own Makefile so
# `make setup` here produces exactly what `make build && make devrc` at
# the repo root would produce.
DEV_PLUGIN_DIR   ?= /tmp/phala-tf-dev
DEV_TF_CLI_CONFIG := $(DEV_PLUGIN_DIR)/terraformrc
DEV_BINARY       := $(DEV_PLUGIN_DIR)/terraform-provider-phala

TF_TESTS_DIR     := $(CURDIR)/terraforms

# Scenario partitioning — kept in sync with terraforms/README.md.
# Free-tier (no CVM allocated, no billing):
FREE_SCENARIOS   := t1-datasources t2-ssh t3-preflight t7-structured-errors
# Billable (each provisions one or more real tdx.small CVMs):
BILLABLE_SCENARIOS := t4-app-lifecycle t5-mig-cluster t6-replicas-removed t8-preflight-vs-real t9-concurrent-nonce-race
ALL_SCENARIOS    := $(FREE_SCENARIOS) $(BILLABLE_SCENARIOS)

.PHONY: help setup build devrc check-env list clean clean-all

help:
	@echo "terraform-provider-phala — e2e test harness"
	@echo ""
	@echo "Setup:"
	@echo "  make setup         Build provider binary + write dev_overrides config"
	@echo "  make build         Rebuild provider binary only ($(DEV_BINARY))"
	@echo "  make devrc         Rewrite dev_overrides config only ($(DEV_TF_CLI_CONFIG))"
	@echo "  make check-env     Verify PHALA_CLOUD_API_KEY, TF_CLI_CONFIG_FILE, binary"
	@echo ""
	@echo "Scenarios (read terraforms/<dir>/README.md for details):"
	@echo "  Free:      $(FREE_SCENARIOS)"
	@echo "  Billable:  $(BILLABLE_SCENARIOS)"
	@echo "  make list  Print scenario list with one path per line"
	@echo ""
	@echo "Cleanup:"
	@echo "  make clean         Remove .terraform/, lock, state from every scenario"
	@echo "  make clean-all     Also remove $(DEV_PLUGIN_DIR) (binary + devrc)"
	@echo ""
	@echo "Run a scenario manually:"
	@echo "  export PHALA_CLOUD_API_KEY=<key>"
	@echo "  export TF_CLI_CONFIG_FILE=$(DEV_TF_CLI_CONFIG)"
	@echo "  cd terraforms/t1-datasources && terraform plan"

setup: build devrc
	@echo ""
	@echo "Setup complete. Next:"
	@echo "  export TF_CLI_CONFIG_FILE=$(DEV_TF_CLI_CONFIG)"
	@echo "  export PHALA_CLOUD_API_KEY=<your-api-key>"
	@echo "  make check-env"

build:
	@$(MAKE) -C $(PROVIDER_DIR) build

devrc:
	@$(MAKE) -C $(PROVIDER_DIR) devrc

check-env:
	@status=0; \
	if [ -z "$$PHALA_CLOUD_API_KEY" ]; then \
		echo "MISSING: PHALA_CLOUD_API_KEY"; status=1; \
	else echo "OK: PHALA_CLOUD_API_KEY set"; fi; \
	if [ -z "$$TF_CLI_CONFIG_FILE" ]; then \
		echo "MISSING: TF_CLI_CONFIG_FILE — export TF_CLI_CONFIG_FILE=$(DEV_TF_CLI_CONFIG)"; status=1; \
	elif [ "$$TF_CLI_CONFIG_FILE" != "$(DEV_TF_CLI_CONFIG)" ]; then \
		echo "WARN: TF_CLI_CONFIG_FILE=$$TF_CLI_CONFIG_FILE (expected $(DEV_TF_CLI_CONFIG))"; \
	else echo "OK: TF_CLI_CONFIG_FILE=$$TF_CLI_CONFIG_FILE"; fi; \
	if [ ! -x "$(DEV_BINARY)" ]; then \
		echo "MISSING: provider binary at $(DEV_BINARY) — run make build"; status=1; \
	else echo "OK: provider binary at $(DEV_BINARY)"; fi; \
	if [ ! -f "$(DEV_TF_CLI_CONFIG)" ]; then \
		echo "MISSING: dev_overrides config at $(DEV_TF_CLI_CONFIG) — run make devrc"; status=1; \
	else echo "OK: dev_overrides config at $(DEV_TF_CLI_CONFIG)"; fi; \
	exit $$status

list:
	@for s in $(ALL_SCENARIOS); do echo "terraforms/$$s"; done

clean:
	@for d in $(ALL_SCENARIOS); do \
		dir="$(TF_TESTS_DIR)/$$d"; \
		if [ -d "$$dir" ]; then \
			rm -rf "$$dir/.terraform" \
			       "$$dir/.terraform.lock.hcl" \
			       "$$dir/terraform.tfstate" \
			       "$$dir/terraform.tfstate.backup"; \
			echo "cleaned $$dir"; \
		fi; \
	done

clean-all: clean
	@rm -rf $(DEV_PLUGIN_DIR)
	@echo "removed $(DEV_PLUGIN_DIR)"
