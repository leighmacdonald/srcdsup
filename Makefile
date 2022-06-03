BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
TAGGED_IMAGE = leighmacdonald/stvup:$(BRANCH)

image:
	@docker build -t $(TAGGED_IMAGE) .

publish: image
	@docker push $(TAGGED_IMAGE)