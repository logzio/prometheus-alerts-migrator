IMAGE_NAME := prometheus-alerts-migrator
IMAGE_TAG := v1.0.0-test
DOCKER_REPO := logzio/$(IMAGE_NAME):$(IMAGE_TAG)
K8S_NAMESPACE := monitoring

.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_REPO) .

.PHONY: docker-push
docker-push:
	docker push $(DOCKER_REPO)

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

.PHONY: format-components
format-components:
	$(MAKE) install-tools
	$(MAKE) for-all CMD="make fmt"

.PHONY: lint-components
lint-components:
	$(MAKE) install-tools
	$(MAKE) for-all CMD="make lint"

.PHONY: for-all
for-all:
	@set -e; for dir in $(ALL_MODULES); do \
	  (cd "$${dir}" && \
	  	echo "running $${CMD} in $${dir}" && \
	 	$${CMD} ); \
	done
