NAME_SERVER=server
NAME_JOB_WORKER=job_worker
NAME_QUEUE_RECOVERY=queue_recovery
BUILD_DIR ?= bin
BUILD_SRC_SERVER=./cmd/server
BUILD_SRC_WORKER=./cmd/worker
BUILD_SRC_QUEUE_RECOVERY=./cmd/recovery
COMMIT_SHORT_HASH = $(shell git rev-parse --short HEAD)
DATE = $(shell date -u +%Y.%m.%d-%H%M%S)
VERSION = v$(DATE)-$(COMMIT_SHORT_HASH)

NO_COLOR=\033[0m
OK_COLOR=\033[32;01m
ERROR_COLOR=\033[31;01m
WARN_COLOR=\033[33;01m

.PHONY: deps test build all
all: deps test build

deps:
	go mod download
build:
	@echo "$(OK_COLOR)==> Building the server binary ...$(NO_COLOR)"
	@CGO_ENABLED=0 go build -v -ldflags="-s -w" -o "$(BUILD_DIR)/$(NAME_SERVER)" "$(BUILD_SRC_SERVER)"
	@echo "$(OK_COLOR)==> Building the job worker cmd ...$(NO_COLOR)"
	@CGO_ENABLED=0 go build -v -ldflags="-s -w" -o "$(BUILD_DIR)/$(NAME_JOB_WORKER)" "$(BUILD_SRC_WORKER)"
	@echo "$(OK_COLOR)==> Building the queue recovery cmd ...$(NO_COLOR)"
	@CGO_ENABLED=0 go build -v -ldflags="-s -w" -o "$(BUILD_DIR)/$(NAME_QUEUE_RECOVERY)" "$(BUILD_SRC_QUEUE_RECOVERY)"
test:
	@echo "$(OK_COLOR)==> Running the unit tests and integration tests $(NO_COLOR)"
	@godotenv -f .env go test -race -tags unit -cover ./...
docker:
	docker build -t task-manager:$(VERSION) .
	echo "Image is built and tagged as :"task-manager:$(VERSION)