SHELL := /bin/bash

# not generating any local files
.PHONY: run image build kind_load rollout build-and-deploy-local

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o volume-admission .

image:
	bash -c "source <(minikube docker-env) || : && docker build --target image -f .pipeline/blubber.yaml . -t volume-admission:dev"

kind_load:
	bash -c "hash kind 2>/dev/null && kind load docker-image docker.io/library/volume-admission:dev --name toolforge || :"

rollout:
	kubectl rollout restart -n volume-admission deployment volume-admission

build-and-deploy-local: image kind_load rollout
	./deploy.sh local
