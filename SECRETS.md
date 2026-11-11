# Secrets Management

**Never commit secrets to this repo.**

## Required Environment Variables

| Variable | Description | Where to get it |
|----------|-------------|-----------------|
| `DATABASE_URL` | Postgres connection string | your PostgreSQL server (`install.py` creates the database and writes it) |
| `RESEND_API_KEY` | Transactional email API key | https://resend.com/api-keys |

## Setting Secrets

### Local Development

Create a `.env` file (gitignored):

```bash
cp .env.example .env
# Edit .env with your values
```

## Files

- `.env` — Local secrets (gitignored)
- `.env.example` — Template showing required vars (safe to commit)

## Security Rules

1. **Never commit `.env`** — it's in .gitignore
2. **Keep production secrets in the host's environment settings**, never in files
3. **Rotate keys** if accidentally exposed
4. **Reference env vars in code** via `os.Getenv()`
