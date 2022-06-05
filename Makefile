BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
TAGGED_IMAGE = ghcr.io/leighmacdonald/stvup:$(BRANCH)

image:
	@docker build -t $(TAGGED_IMAGE) .

publish: image
	@docker push $(TAGGED_IMAGE)