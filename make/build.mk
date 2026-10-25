build:
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) build -o $(BINARY) ./cmd/facet

verify-mod:
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) mod verify
