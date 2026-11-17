default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	go generate ./internal/client
	terraform fmt -recursive examples/
	cd tools; go tool tfplugindocs generate --provider-dir .. -provider-name vibeoncall

CLIENT_SPEC = internal/client/openapi.yaml
AI_SERVER_DIR ?= ../ai-server

define update_client_from
	tmp=$$(mktemp) && gh api -H 'Accept: application/vnd.github.raw' 'repos/vibraniumlabs/ai-server/contents/openapi/openapi.yaml?ref=$(1)' > $$tmp && mv $$tmp $(CLIENT_SPEC)
	go generate ./internal/client
endef

update-client:
	$(call update_client_from,dev)

update-client-main:
	$(call update_client_from,main)

update-client-local:
	cp $(AI_SERVER_DIR)/openapi/openapi.yaml $(CLIENT_SPEC)
	go generate ./internal/client

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: fmt lint test testacc build install generate update-client update-client-main update-client-local
