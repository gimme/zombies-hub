# Makefile - the usual ways to run, build and test. `make` lists the targets. Each target runs a script in docker/,
# which does the work and takes more options.
#
# ARGS is passed to the target's script: make dev ARGS="zombie_pentagon +set g_debug 1", make test ARGS="coop solo".
# REGISTRY is the registry that make push pushes the image to: make push REGISTRY=192.168.0.123 (port 5000 unless
# REGISTRY names another).

ARGS ?=
REGISTRY ?=
.DEFAULT_GOAL := help
.PHONY: help dev web image push launcher-key test test-go test-e2e
# The targets run one at a time and in order.
.NOTPARALLEL:

# make push reports that it needs REGISTRY before it builds the image, not after.
ifneq ($(filter push,$(MAKECMDGOALS)),)
ifeq ($(strip $(REGISTRY)),)
$(error make push needs the registry: make push REGISTRY=<host>[:port], port 5000 by default)
endif
endif

help: ## this list
	@awk -F':.*## ' '/^[a-z0-9-]+:.*## /{printf "  make %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo 'ARGS goes to the script: make dev ARGS="zombie_pentagon +set g_debug 1", make test ARGS="coop solo"'

dev: ## run a dev server on http://localhost:3075/ (docker/server.sh)
	docker/server.sh $(ARGS)

web: ## build the page into web/dist, which make dev serves (docker/web.sh)
	docker/web.sh

image: ## build the server image zombies:latest (docker/image.sh)
	docker/image.sh $(ARGS)

push: image ## make image, then push it to a registry: make push REGISTRY=<host>[:port] (docker/push.sh)
	docker/push.sh $(REGISTRY) $(ARGS)

launcher-key: ## make the key the launcher's updates are signed with, once (docker/launcher-key.sh)
	docker/launcher-key.sh

test: ## test the server image with real games, in this machine's Chrome (docker/test-games.sh)
	docker/test-games.sh $(ARGS)

test-go: ## test the Go code alone, in about a minute (docker/test-go.sh)
	docker/test-go.sh $(ARGS)

test-e2e: ## test the page against the server with stand-in games, in about three minutes (docker/test-e2e.sh)
	docker/test-e2e.sh $(ARGS)
