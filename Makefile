GOFMT_FILES?=$$(find . -name '*.go' | grep -v vendor)
GOFMT := "goimports"

dep:
	go install github.com/golang/mock/mockgen@v1.6.0
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1

gen:
	go generate ./...

build:
	go build -o bin/server cmd/sfu/main.go

run:
	go run cmd/sfu/main.go

fmt: ## Run gofmt for all .go files
	@$(GOFMT) -w $(GOFMT_FILES)

test: ## Run go test for whole project
	@go test -v ./...

lint: ## Run linter
	@golangci-lint run ./...

docker-build:
	docker build . -t webrtc-media-server

docker-run:
	docker run -p 8080:8080 webrtc-media-server

help: ## Display this help screen
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'