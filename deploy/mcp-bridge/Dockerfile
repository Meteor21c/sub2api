FROM node:22-alpine

WORKDIR /app
COPY package.json server.mjs fzy-asset-queue.mjs video-cache.mjs video-delivery.mjs public-fetch.mjs ./
COPY test ./test

RUN mkdir -p /data/materials /data/assets /data/tasks /data/video-cache \
  && chown -R node:node /app /data
USER node

ENV NODE_ENV=production
ENV NODE_OPTIONS=--max-old-space-size=256
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD node -e "fetch('http://127.0.0.1:3102/ready').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"
EXPOSE 3102
CMD ["node", "server.mjs"]
