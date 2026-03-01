APP_NAME := orchestrator
BIN_DIR := bin
CMD_PATH := ./cmd/orchestrator
IMAGE ?= hivemind:latest

.PHONY: build test vet run docker-build deploy

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) $(CMD_PATH)

test:
	go test ./...

vet:
	go vet ./...

run:
	go run $(CMD_PATH)

docker-build:
	docker build -t $(IMAGE) .

deploy:
	kubectl apply -f deploy/
