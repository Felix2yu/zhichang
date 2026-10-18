# 知常 · 装配式镜像：只做拼装，编译全部留给 CI
#
# 二进制由 CI 预编译（CGO_ENABLED=0，静态链接）到 bin/zhichangd，前端产物在编译时
# 已通过 go:embed 打进同一个二进制，因此镜像里既不需要 Go 也不需要 Node 工具链。
# 本地构建前必须先产出二进制：make build（CI 同样如此，缺产物时 COPY 直接失败）。
#
# 基镜 ghcr.io/felix2yu/base:alpine-3.24 提供 ca-certificates、su-exec、tzdata
# 与 PUID/PGID 降权入口（容器内的自然日不能跟着 UTC 走，tzdata 不可缺）。
FROM ghcr.io/felix2yu/base:alpine-3.24

RUN mkdir -p /data

COPY bin/zhichangd /usr/local/bin/zhichangd

ENV ZHICHANG_HTTP_ADDR=:8787 \
	ZHICHANG_HOME_TZ=Asia/Shanghai \
	ZHICHANG_RAW_ARCHIVE_DIR=/data/archive \
	APP_DATA_DIR=/data
# 单独一行：容器内的自然日（聚合、归档目录）不能跟着镜像默认的 UTC 走
ENV TZ=Asia/Shanghai

EXPOSE 8787
VOLUME ["/data"]

# 探活端点必须免鉴权；start-period 留给首次启动的迁移与指标字典写入
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
	CMD wget -q -O- http://127.0.0.1:8787/api/health >/dev/null || exit 1

CMD ["zhichangd"]
