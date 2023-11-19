IMAGE_NAME := prometheus-alerts-migrator
IMAGE_TAG := v1.0.0-test
DOCKER_REPO := logzio/$(IMAGE_NAME):$(IMAGE_TAG)


.PHONY: docker-buildx
docker-buildx:
	docker buildx create --use
	docker buildx build --platform linux/amd64,linux/arm64 -t $(DOCKER_REPO) --push .

.PHONY: run-local
run-local:
	go run main.go

.PHONY: install-tools
install-tools:
	go install github.com/google/addlicense@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/client9/misspell/cmd/misspell@latest
	go install github.com/pavius/impi/cmd/impi@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.47.3
