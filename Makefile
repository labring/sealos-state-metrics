# 镜像配置
IMAGE_NAME ?= sealos-state-metrics
IMAGE_TAG ?= latest
IMAGE_REGISTRY ?= ghcr.io/labring
IMAGE_FULL = $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

# 构建平台
PLATFORM ?= linux/amd64

.PHONY: help
help: ## 显示帮助信息
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## 构建 Docker 镜像
	docker build -t $(IMAGE_FULL) .

.PHONY: build-multiarch
build-multiarch: ## 构建多架构镜像 (amd64/arm64)
	docker buildx build --platform linux/amd64,linux/arm64 -t $(IMAGE_FULL) .

.PHONY: push
push: ## 推送镜像到仓库
	docker push $(IMAGE_FULL)

.PHONY: build-push
build-push: ## 构建并推送多架构镜像
	docker buildx build --platform linux/amd64,linux/arm64 -t $(IMAGE_FULL) --push .

.PHONY: clean
clean: ## 清理本地镜像
	docker rmi $(IMAGE_FULL) || true

.DEFAULT_GOAL := help
