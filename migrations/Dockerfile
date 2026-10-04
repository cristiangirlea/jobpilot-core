# jobpilot-migrate: the SQL files and migrate.sh, on the same PostgreSQL major version as compose and
# CI. Compose's migrate service runs it; in Kubernetes it is the API's init container.
FROM postgres:16-alpine
COPY *.sql /migrations/
COPY migrate.sh /usr/local/bin/jobpilot-migrate
ENV MIGRATIONS_DIR=/migrations
# The image's postgres user (uid 70), numeric so Kubernetes can check runAsNonRoot.
USER 70:70
ENTRYPOINT ["/bin/sh", "/usr/local/bin/jobpilot-migrate"]
