FROM docker.io/library/postgres:12
LABEL maintainer="mcp_demo"

ENV POSTGRES_DB=northwind
ENV POSTGRES_USER=northwind
ENV POSTGRES_PASSWORD=northwind

ADD https://raw.githubusercontent.com/bradymholt/docker-postgresql-northwind/master/01-northwind.sql /docker-entrypoint-initdb.d/01-northwind.sql
RUN chmod 0644 /docker-entrypoint-initdb.d/01-northwind.sql
