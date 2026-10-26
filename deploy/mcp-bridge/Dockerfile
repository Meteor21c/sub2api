FROM node:22-alpine

WORKDIR /app
COPY package.json server.mjs ./
COPY test ./test

RUN mkdir -p /data/materials /data/assets /data/tasks \
  && chown -R node:node /app /data
USER node

ENV NODE_ENV=production
EXPOSE 3102
CMD ["node", "server.mjs"]
