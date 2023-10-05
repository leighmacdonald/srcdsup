BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
TAGGED_IMAGE = leighmacdonald/srcdsup:$(BRANCH)

all: bin

fmt:
	gci write . --skip-generated -s standard -s default
	gofumpt -l -w .

bin:
	@go build -o build/srcdsup

image:
	@docker build -t $(TAGGED_IMAGE) .

publish: image
	@docker push $(TAGGED_IMAGE)

check: lint_golangci static

static:
	staticcheck -go 1.21 ./...

lint_golangci:
	golangci-lint run --timeout 10m ./...

