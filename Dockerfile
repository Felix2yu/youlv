# 「游履」运行时镜像：二进制由 CI 预编译并下载到 bin/ 后拼装。
#
# 编译期依赖（node_modules、Go 工具链）全部留在 CI，不进镜像。
# 前端产物已通过 go:embed 内嵌进二进制，镜像里不再有 web 目录。
#
# 基镜 ghcr.io/felix2yu/base:alpine-3.24 已提供 ca-certificates、tzdata、su-exec
# 与 PUID/PGID 降权入口（ENTRYPOINT /usr/local/bin/docker-entrypoint.sh）：
# 因此这里不再 apk add，也不再自带 scripts/docker-entrypoint.sh。
# 契约见 Felix2yu/.github/docs/docker.md。

FROM ghcr.io/felix2yu/base:alpine-3.24

WORKDIR /app

# 容器内布局（全 fleet 统一）：二进制放 /app/youlv，数据放 /app/data 挂载卷。
# /app 不在 PATH 上，所以外部备份工具的 BACKUP_SQLITE_CMD 必须写绝对路径
# `/app/youlv backup`，裸名 `youlv backup` 在 docker exec 里会 command not found。
# /app/data 由基镜入口按 PUID/PGID chown，容器重建不丢数据。
ENV YOULV_DATA_DIR=/app/data

COPY --chmod=755 bin/youlv /app/youlv

RUN mkdir -p /app/data
VOLUME ["/app/data"]

EXPOSE 8802

# 健康检查走 /api/health
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8802/api/health >/dev/null || exit 1

# ENTRYPOINT 由基镜提供（PUID/PGID 降权后 exec 本命令），这里只给服务命令
CMD ["/app/youlv", "-addr", ":8802"]
