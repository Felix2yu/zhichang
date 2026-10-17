#!/bin/sh
# 按 PUID/PGID 对齐宿主机目录属主后降权运行，避免容器内 root 写出 root 属主文件
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

TARGET_UID=$(id -u zhichang)
TARGET_GID=$(id -g zhichang)

if [ "$PUID" != "$TARGET_UID" ] || [ "$PGID" != "$TARGET_GID" ]; then
	groupmod -o -g "$PGID" zhichang
	usermod -o -u "$PUID" zhichang
fi

mkdir -p "${ZHICHANG_RAW_ARCHIVE_DIR:-/data/archive}"
chown -R "${PUID}:${PGID}" /data

exec su-exec zhichang "$@"
