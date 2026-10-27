FROM node:22-alpine
WORKDIR /app
COPY --chown=node:node package.json server.mjs ./
USER node
ENV NODE_ENV=production PORT=3103
EXPOSE 3103
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD node -e "fetch('http://127.0.0.1:3103/health').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"
CMD ["node", "server.mjs"]
