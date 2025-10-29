#!/bin/bash

# Caddy Traffic Mirror 模块编译脚本

set -e

echo "🔨 开始编译 Caddy (含流量复制模块)..."

# 检查 xcaddy 是否安装
if command -v xcaddy &> /dev/null; then
    XCADDY="xcaddy"
elif [ -f "$HOME/go/bin/xcaddy" ]; then
    XCADDY="$HOME/go/bin/xcaddy"
else
    echo "❌ xcaddy 未安装"
    echo "请先安装 xcaddy:"
    echo "  go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest"
    exit 1
fi

# 编译本机版本
echo "📦 正在编译本机版本..."
$XCADDY build \
    --with github.com/baogaitou/caddy-traffic-mirror=./

if [ $? -ne 0 ]; then
    echo "❌ 本机版本编译失败"
    exit 1
fi
echo "✅ 本机版本编译成功! (./caddy)"
echo ""

# 交叉编译 Linux (amd64) 版本
echo "🚀 开始交叉编译 Linux (amd64) 版本..."
GOOS=linux GOARCH=amd64 $XCADDY build \
    --output caddy-linux \
    --with github.com/baogaitou/caddy-traffic-mirror=./

if [ $? -ne 0 ]; then
    echo "❌ Linux 版本编译失败"
    exit 1
fi
echo "✅ Linux (amd64) 版本编译成功! (./caddy-linux)"
echo ""


echo "🎉 所有编译任务完成!"
echo ""
echo "可执行文件:"
echo "  - ./caddy        (用于当前操作系统)"
echo "  - ./caddy-linux  (用于 Linux amd64)"
echo ""
echo "验证模块加载 (使用本机版本):"
./caddy list-modules | grep traffic_mirror
echo ""
echo "使用方法:"
echo "  ./caddy run --config Caddyfile"
echo ""
echo "查看完整文档: README.md"