BINARY := bin/facet
GO ?= go
GOTOOLCHAIN ?= auto

.PHONY: build verify-mod fmt-check test test-race vet check ci package clean
