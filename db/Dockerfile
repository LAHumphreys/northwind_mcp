FROM docker.io/library/postgres:16
LABEL maintainer="mcp_demo"

ENV POSTGRES_DB=northwind
ENV POSTGRES_USER=northwind
ENV POSTGRES_PASSWORD=northwind

# Seed SQL is vendored under db/init so builds are reproducible and offline.
# Upstream: https://github.com/bradymholt/docker-postgresql-northwind
COPY --chmod=0644 init/01-northwind.sql /docker-entrypoint-initdb.d/01-northwind.sql
