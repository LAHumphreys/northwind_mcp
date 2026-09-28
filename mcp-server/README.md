# Northwind MCP Server

A minimal Go MCP server that exposes the Northwind Postgres database through
resources served over Streamable HTTP.

## 1. Build and run the Northwind database with Podman

The [`../db/Dockerfile`](../db/Dockerfile) builds a Postgres image seeded with
the Northwind sample data (`POSTGRES_USER`/`POSTGRES_PASSWORD` both `northwind`).
Building it locally means the DB can be rebuilt/restarted without depending on
a Docker Hub pull each time.

```sh
cd ../db
podman build -t northwind-db:local .
podman run -d --name northwind-db -p 5432:5432 northwind-db:local
```

Verify the data loaded (init scripts take a few seconds to run on first start):

```sh
podman exec northwind-db psql -U northwind -d northwind -c "select count(*) from products;"
# => 77
```

### Restarting the DB later

```sh
podman restart northwind-db          # reuse the existing container/data
# or, to rebuild the image and start fresh:
podman stop northwind-db && podman rm northwind-db
podman build -t northwind-db:local ../db
podman run -d --name northwind-db -p 5432:5432 northwind-db:local
```

## 2. Configure and run the server

Environment variables (all optional, defaults shown):

| Variable                | Default       |
| ------------------------ | ------------- |
| `NORTHWIND_DB_HOST`      | `localhost`   |
| `NORTHWIND_DB_PORT`      | `5432`        |
| `NORTHWIND_DB_USER`      | `northwind`   |
| `NORTHWIND_DB_PASSWORD`  | `northwind`   |
| `NORTHWIND_DB_NAME`      | `northwind`   |
| `MCP_HTTP_ADDR`          | `:8080`       |

```sh
NORTHWIND_DB_PASSWORD=northwind go run .
# northwind-mcp-server listening at :8080/mcp
```

## 3. Test with curl

```sh
curl -s -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

curl -s -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"northwind://products"}}'

curl -s -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"northwind://customers/ALFKI"}}'

curl -s -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"northwind://customers"}}'
```

The `get_products` resource (URI `northwind://products`) returns a JSON array
of every row in the `products` table. The `get_customers` resource (URI
`northwind://customers`) returns a JSON array of every customer row. The
`get_customer_info` resource template uses URIs of the form
`northwind://customers/{customer_id}` and returns the customer's contact
details, total distinct orders, lifetime line-item value, and last order date
as JSON.
