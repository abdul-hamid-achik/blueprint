FROM node:22-slim AS builder
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci

COPY . .
RUN npx tsc

FROM node:22-slim
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --omit=dev

COPY --from=builder /app/dist ./dist

RUN addgroup --system app && adduser --system --ingroup app app
USER app

EXPOSE 3000
CMD ["node", "dist/index.js"]
