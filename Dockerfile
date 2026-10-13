# 知常 · 多阶段构建：前端 → 后端 → 最小运行镜像（单二进制 + 内嵌前端）
FROM node:26-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@12.11.2 --registry=https://registry.npmjs.org
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS server
WORKDIR /src
ENV CGO_ENABLED=0
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build ./web/build
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/zhichangd ./cmd/zhichangd \
	&& go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/zhichangctl ./cmd/zhichangctl \
	&& go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/zhichang-mcp ./cmd/zhichang-mcp

FROM alpine:3.22
RUN apk add --no-cache ca-certificates su-exec tzdata \
	&& addgroup -g 1000 zhichang \
	&& adduser -D -u 1000 -G zhichang zhichang \
	&& mkdir -p /data && chown zhichang:zhichang /data
COPY --from=server /out/zhichangd /out/zhichangctl /out/zhichang-mcp /usr/local/bin/
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV ZHICHANG_HTTP_ADDR=:8787 \
	ZHICHANG_HOME_TZ=Asia/Shanghai \
	ZHICHANG_RAW_ARCHIVE_DIR=/data/archive \
	PUID=1000 \
	PGID=1000

EXPOSE 8787
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["zhichangd"]
