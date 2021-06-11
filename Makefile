
IMAGE_NAME := mockmate_v1

LOCAL_PORT := 8080

.PHONY: test

check-project:
ifndef PROJECT
	$(error missing PROJECT)
endif

test:
	go test

run:
	export PORT=$(LOCAL_PORT) && \
	go run server.go

build-local: test
	pack build $(IMAGE_NAME) \
		--builder gcr.io/buildpacks/builder:v1

build: check-project test
	gcloud builds submit \
		--project=$(PROJECT) \
		--pack image=gcr.io/$(PROJECT)/$(IMAGE_NAME)

build-async: check-project test
	gcloud builds submit \
		--project=$(PROJECT) \
		--async \
		--pack image=gcr.io/$(PROJECT)/$(IMAGE_NAME)
