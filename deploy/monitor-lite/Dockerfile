FROM node:22-bookworm-slim
ENV NODE_ENV=production \
    NODE_OPTIONS="--max-old-space-size=48 --max-semi-space-size=4" \
    UV_THREADPOOL_SIZE=2 \
    TZ=Asia/Shanghai
WORKDIR /app
COPY lite/*.mjs ./lite/
COPY --chmod=755 docker/vps-lite-entrypoint.sh /usr/local/bin/meteor-monitor-entrypoint
EXPOSE 3100
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["node", "-e", "fetch('http://127.0.0.1:3100/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
ENTRYPOINT ["/usr/local/bin/meteor-monitor-entrypoint"]
