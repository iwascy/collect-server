APP := infohub
DOCKER_CONTEXT ?=
DOCKER_CONTEXT_ARG := $(if $(DOCKER_CONTEXT),--context $(DOCKER_CONTEXT),)
ESPHOME_ENV_FILE := deploy/esphome/docker/.env
ESPHOME_ENV_ARG := $(if $(wildcard $(ESPHOME_ENV_FILE)),--env-file $(ESPHOME_ENV_FILE),)
ESPHOME_COMPOSE := docker $(DOCKER_CONTEXT_ARG) compose -f deploy/esphome/docker/compose.yaml $(ESPHOME_ENV_ARG)

.PHONY: build run fmt test tidy esphome-config esphome-up esphome-down esphome-logs esphome-ps esphome-pull esphome-compile-stage1 esphome-compile-stage1-alt esphome-compile-stage2 esphome-compile-partial-probe esphome-recreate

build:
	go build -o bin/$(APP) ./cmd/infohub

run:
	go run ./cmd/infohub -config config.yaml

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

tidy:
	go mod tidy

esphome-config:
	$(ESPHOME_COMPOSE) config

esphome-up:
	$(ESPHOME_COMPOSE) up -d

esphome-down:
	$(ESPHOME_COMPOSE) down

esphome-logs:
	$(ESPHOME_COMPOSE) logs -f --tail=200

esphome-ps:
	$(ESPHOME_COMPOSE) ps

esphome-pull:
	$(ESPHOME_COMPOSE) pull

esphome-recreate:
	$(ESPHOME_COMPOSE) down
	$(ESPHOME_COMPOSE) up -d --force-recreate

esphome-compile-stage1:
	$(ESPHOME_COMPOSE) exec esphome /entrypoint.sh compile /config/reterminal_e1001_first_flash.yaml

esphome-compile-stage1-alt:
	$(ESPHOME_COMPOSE) exec esphome /entrypoint.sh compile /config/reterminal_e1001_first_flash_alt.yaml

esphome-compile-stage2:
	$(ESPHOME_COMPOSE) exec esphome /entrypoint.sh compile /config/reterminal_e1001_infohub_api.yaml

esphome-compile-partial-probe:
	$(ESPHOME_COMPOSE) exec esphome /entrypoint.sh compile /config/reterminal_e1001_partial_refresh_probe.yaml
