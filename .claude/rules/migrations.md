---
paths:
  - "migrations/**"
---
# Migrations

- Add a new numbered file after the highest in `migrations/`; never rewrite one that has been
  applied. Check `master` for a newer number before handing over: other sessions add them too.
- Make it safe to rerun where practical (`IF NOT EXISTS`, guarded constraints, conflict handling).
- No `BEGIN` or `COMMIT` in a file: `migrate.sh` wraps each one in its own transaction and rejects
  a file that does.
- Add the file to the migration table in `README.md` ("Database and migrations").
- Try it on a throwaway `postgres:16-alpine` container with `DATABASE_URL=... sh migrations/migrate.sh`,
  never on the owner's database (`make migrate` targets his Compose stack).
