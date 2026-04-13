.PHONY: serve build clean help

.DEFAULT_GOAL := serve

## serve: serve Hugo docs site locally at http://localhost:1313
serve:
	@echo "Starting Hugo development server..."
	hugo server -D --port 1311

## build: build Hugo docs site to public/
build:
	@echo "Building Hugo docs site..."
	hugo --gc --minify

## clean: remove generated files
clean:
	@echo "Cleaning generated files..."
	rm -rf public resources .hugo_build.lock

## help: print this list
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
