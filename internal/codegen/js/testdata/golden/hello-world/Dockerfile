FROM node:22-slim AS base
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --production

COPY . .

RUN addgroup --system app && adduser --system --ingroup app app
USER app

EXPOSE 3000
CMD ["npx", "tsx", "src/index.ts"]
