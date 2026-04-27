# Deployment Guide

How to deploy a Blueprint-generated service to production.

## Quick Start

```bash
# Build the service
bp build my-service.bp

# Move into generated project
cd generated

# Configure environment
cp .env.example .env
# Edit .env with production values

# Install dependencies
bun install

# Push database schema (or run migrations)
bunx drizzle-kit push

# Start the server
bun run start
```

---

## Docker

Every `bp build` generates a `Dockerfile` alongside your project.

### Build and Run Locally

```bash
bp build my-service.bp
cd generated

docker build -t my-service .
docker run \
  -e DATABASE_URL=postgresql://user:pass@host/db \
  -e STRIPE_KEY=sk_live_... \
  -p 3000:3000 \
  my-service
```

### Docker Compose

For local development with a database:

```yaml
# docker-compose.yml
version: '3.8'

services:
  app:
    build: ./generated
    ports:
      - '3000:3000'
    environment:
      DATABASE_URL: postgresql://postgres:postgres@db:5432/mydb
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: mydb
      POSTGRES_PASSWORD: postgres
    ports:
      - '5432:5432'
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ['CMD-SHELL', 'pg_isready -U postgres']
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  pgdata:
```

```bash
docker compose up -d
```

### Multi-stage Build

The generated `Dockerfile` uses a multi-stage build pattern:

```dockerfile
FROM oven/bun:1 AS builder
WORKDIR /app
COPY package.json bun.lock* ./
RUN bun install
COPY . .

FROM oven/bun:1
WORKDIR /app
COPY --from=builder /app .
EXPOSE 3000
CMD ["bun", "run", "start"]
```

---

## Database Migrations

### Development: Schema Push

For rapid development, push schema changes directly:

```bash
bp migrate my-service.bp push
```

This uses `drizzle-kit push` under the hood. No migration files — changes are applied directly.

### Production: Migration Files

For production, generate and apply migration files:

```bash
# Generate a migration from schema changes
bp migrate my-service.bp generate

# Apply pending migrations
cd generated
bunx drizzle-kit migrate
```

Migration files land in `generated/drizzle/` and should be committed to version control.

### Database Studio

Inspect your database visually:

```bash
bp migrate my-service.bp studio
```

Opens Drizzle Studio at `https://local.drizzle.studio`.

---

## Environment Variables

Blueprint services require environment variables declared as `secret` and `env` in your `.bp` file.

### Required Secrets

Secrets declared with `required` cause a startup error if missing:

```bp
secret DATABASE_URL required
secret STRIPE_KEY   required
```

### Optional Secrets

Secrets with `optional` or `default(...)` are safe to omit:

```bp
secret WEBHOOK_KEY optional default("")
```

### Env Vars with Defaults

`env` declarations have baked-in defaults but can be overridden at runtime:

```bp
env MAX_FILE_SIZE  10mb
env LOG_LEVEL      "info"
```

### `.env.example`

Every build generates a `.env.example` listing all declared secrets and env vars. Copy this as your starting point:

```bash
cp .env.example .env
```

---

## Cloud Platforms

### Fly.io

```bash
bp build my-service.bp
cd generated

# Initialize Fly app
fly launch --no-deploy

# Set secrets
fly secrets set DATABASE_URL=postgresql://...
fly secrets set STRIPE_KEY=sk_live_...

# Deploy
fly deploy
```

### Railway

```bash
bp build my-service.bp
cd generated

# Push to a git repo, then connect Railway to the repo
git init && git add -A && git commit -m "Initial deploy"

# Railway auto-detects the Dockerfile and deploys
```

Set environment variables in the Railway dashboard under **Variables**.

### Vercel

Vercel works best for serverless deployments. The generated Hono app runs on Vercel's Node.js runtime.

```bash
bp build my-service.bp
cd generated
```

Add a `vercel.json` to the generated directory:

```json
{
  "builds": [{ "src": "src/index.ts", "use": "@vercel/node" }],
  "routes": [{ "src": "/(.*)", "dest": "src/index.ts" }]
}
```

Then deploy:

```bash
bunx vercel
```

Set environment variables in the Vercel dashboard under **Settings > Environment Variables**, or via CLI:

```bash
bunx vercel env add DATABASE_URL
bunx vercel env add STRIPE_KEY
```

### Netlify

For Netlify, use the `@hono/netlify` adapter. After building:

```bash
bp build my-service.bp
cd generated
bun add @hono/netlify
```

Add a `netlify.toml`:

```toml
[build]
  command = "bun install"
  publish = "."

[functions]
  directory = "netlify/functions"
```

Create `netlify/functions/api.ts`:

```typescript
import { handle } from '@hono/netlify'
import app from '../../src/index.js'

export default handle(app)
```

Deploy:

```bash
bunx netlify deploy --prod
```

Set environment variables in the Netlify dashboard under **Site settings > Environment variables**.

### Render

1. Push your `generated/` directory to a Git repository
2. Create a new **Web Service** on Render
3. Set the build command to `bun install`
4. Set the start command to `bun run start`
5. Add environment variables in the Render dashboard

### Generic VPS (Ubuntu)

```bash
# On the server
bp build my-service.bp
cd generated
bun install

# Use PM2 for process management
bun add -g pm2
pm2 start bun --name my-service -- run start
pm2 save
pm2 startup
```

---

## Production Checklist

Before going live, verify:

- [ ] All `required` secrets are set in the environment
- [ ] Database migrations have been applied (`bp migrate push` or `bunx drizzle-kit migrate`)
- [ ] The `Dockerfile` builds successfully
- [ ] Health check endpoint is reachable (add a `GET /health` endpoint)
- [ ] Rate limiting is configured on public endpoints
- [ ] CORS origins are set to your actual domain (not `*`)
- [ ] Error responses don't leak internal details
- [ ] Logs are going to a centralized logging service
- [ ] Database connection pool is sized appropriately
- [ ] TLS/HTTPS is terminated at the load balancer or reverse proxy

---

## Health Checks

Add a health check endpoint to your `.bp` file:

```bp
@ "Health check for load balancers and monitoring"
GET /health {
  -> 200 { status: "ok", version: "1.0.0" }
}
```

For database-aware health checks:

```bp
@ "Deep health check including database connectivity"
GET /health/ready {
  |> try {
    |> count = count user
    -> 200 { status: "ok", db: "connected" }
  } recover {
    -> 503 { status: "error", db: "disconnected" }
  }
}
```

---

## Reverse Proxy (Nginx)

If running behind Nginx:

```nginx
server {
    listen 80;
    server_name api.example.com;

    location / {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_cache_bypass $http_upgrade;
    }
}
```

The `Upgrade` and `Connection` headers are important if your service uses WebSocket endpoints (`WS /ws/...`).
