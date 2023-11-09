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
