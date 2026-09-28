# Dev entry points. Override COMPOSE for Podman: make COMPOSE="podman compose" db-up
GO         ?= go
COMPOSE    ?= docker compose
SERVER_DIR := mcp-server
BIN        := bin/mcp-northwind-server

export NORTHWIND_DB_PASSWORD ?= northwind

.PHONY: build run test test-db vet fmt lint db-up db-down db-reset db-psql up down clean

build:
	cd $(SERVER_DIR) && $(GO) build -o ../$(BIN) .

run:
	cd $(SERVER_DIR) && $(GO) run .

test:
	cd $(SERVER_DIR) && $(GO) test -race -count=1 ./...

# Same as test but fails (rather than skips) when the database is unreachable.
test-db:
	cd $(SERVER_DIR) && NORTHWIND_TEST_REQUIRE_DB=1 $(GO) test -race -count=1 ./...

vet:
	cd $(SERVER_DIR) && $(GO) vet ./...

fmt:
	cd $(SERVER_DIR) && gofmt -l -w .

lint: vet
	@cd $(SERVER_DIR) && files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "gofmt: needs formatting:"; echo "$$files"; exit 1; fi

db-up:
	$(COMPOSE) up -d --wait db

db-down:
	$(COMPOSE) down

# Drop the data volume so the seed scripts run again on next db-up.
db-reset:
	$(COMPOSE) down -v

db-psql:
	$(COMPOSE) exec db psql -U northwind -d northwind

up:
	$(COMPOSE) --profile full up -d --build --wait

down:
	$(COMPOSE) --profile full down

clean:
	rm -rf bin
