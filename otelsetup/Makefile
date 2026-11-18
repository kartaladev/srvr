# Targets run from the module directory with GOWORK=off, as released
# modules are built by their consumers.
export GOWORK := off

.PHONY: test lint cover vet

vet:
	go vet ./...

test:
	go test -race ./...

lint:
	golangci-lint run ./...

cover:
	go test -coverprofile=c.out ./... && go tool cover -func=c.out
