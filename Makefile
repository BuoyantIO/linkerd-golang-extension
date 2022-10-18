SHELL=bash

VERSION=0.1.0

help:
	@echo "Just run 'make all'."
	@echo ""
	@echo "You will need to have the DEV_REGISTRY variable set when doing this."
	@echo ""
	@echo "You can also 'make clean' to remove all the Docker-image stuff,"
	@echo "or 'make clobber' to smite everything and completely start over."
.PHONY: help

registry-check:
	@if [ -z "$(DEV_REGISTRY)" ]; then \
		echo "DEV_REGISTRY must be set (e.g. DEV_REGISTRY=docker.io/myregistry)" >&2 ;\
		exit 1; \
	fi
.PHONY: registry-check

all: images cli

clean:
	-rm -f linkerd-gamma
.PHONY: clean

clobber: clean
	true
.PHONY: clobber

images image: registry-check
	docker build -t $(DEV_REGISTRY)/linkerd-gamma:$(VERSION) .
.PHONY: images image

# This is just an alias
cli: linkerd-gamma

linkerd-gamma:
	go build -o linkerd-gamma ./cmd/
.PHONY: cmd/linkerd-gamma

deploy: images k8s/faces-gui.yaml k8s/faces.yaml
	kubectl create namespace faces || true
	kubectl apply -n faces -f k8s
	# @echo "You should now be able to open http://faces.example.com/ in your browser."

# Sometimes we have a file-target that we want Make to always try to
# re-generate (such as compiling a Go program; we would like to let
# `go install` decide whether it is up-to-date or not, rather than
# trying to teach Make how to do that).  We could mark it as .PHONY,
# but that tells Make that "this isn't a real file that I expect to
# ever exist", which has a several implications for Make, most of
# which we don't want.  Instead, we can have them *depend* on a .PHONY
# target (which we'll name "FORCE"), so that they are always
# considered out-of-date by Make, but without being .PHONY themselves.
.PHONY: FORCE
