BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
TAGGED_IMAGE = leighmacdonald/srcdsup:$(BRANCH)

all: bin

bin:
	@go build -o build/srcdsup

image:
	@docker build -t $(TAGGED_IMAGE) .

publish: image
	@docker push $(TAGGED_IMAGE)
