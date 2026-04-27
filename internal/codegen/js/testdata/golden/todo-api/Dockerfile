FROM oven/bun:1 AS builder
WORKDIR /app

COPY package.json bun.lock* ./
RUN bun install

COPY . .
RUN bun run build

FROM oven/bun:1
WORKDIR /app

COPY package.json bun.lock* ./
RUN bun install --production

COPY --from=builder /app/dist ./dist

RUN addgroup --system app && adduser --system --ingroup app app
USER app

EXPOSE 3000
CMD ["bun", "dist/index.js"]
