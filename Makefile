IMAGE_NAME := prometheus-alerts-migrator
IMAGE_TAG := v1.0.0-test
DOCKER_REPO := logzio/$(IMAGE_NAME):$(IMAGE_TAG)
K8S_NAMESPACE := monitoring

# ALL_PKGS is used with 'go cover'
ALL_PKGS := $(shell $(GOCMD) list $(sort $(dir $(ALL_SRC))))
# All source code and documents. Used in spell check.
ALL_SRC_AND_DOC := $(shell find $(ALL_PKG_DIRS) -name "*.md" -o -name "*.go" -o -name "*.yaml" \
                                -not -path '*/third_party/*' \
                                -type f | sort)

MISSPELL=misspell -error
MISSPELL_CORRECTION=misspell -w
LINT=golangci-lint
IMPI=impi

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
