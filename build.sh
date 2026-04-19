#!/bin/bash

# Tlink 交叉编译脚本

set -e

# 输出目录
OUTPUT_DIR="dist"
mkdir -p "$OUTPUT_DIR"

echo "=========================================="
echo "Tlink 交叉编译"
echo "=========================================="
echo ""

# 编译函数
build() {
    GOOS=$1
    GOARCH=$2
    OUTPUT_NAME=$3

    echo "正在编译: $GOOS/$GOARCH"
    echo "输出文件: $OUTPUT_NAME"

    CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -o "$OUTPUT_DIR/$OUTPUT_NAME" .

    if [ $? -eq 0 ]; then
        echo "✓ 编译成功: $OUTPUT_NAME"
    else
        echo "✗ 编译失败: $OUTPUT_NAME"
        exit 1
    fi
    echo ""
}

# 清理旧版本
echo "清理旧的编译文件..."
rm -rf "$OUTPUT_DIR"/*
echo ""

# Linux amd64
build "linux" "amd64" "tlink-linux-amd64"

# Linux arm (armv7)
build "linux" "arm" "tlink-linux-arm"

# Linux arm64 (aarch64)
build "linux" "arm64" "tlink-linux-arm64"

# Termux (aarch64) - 和 linux-arm64 是一样的，单独命名方便识别
cp "$OUTPUT_DIR/tlink-linux-arm64" "$OUTPUT_DIR/tlink-termux-arm64"
echo "✓ 复制完成: tlink-termux-arm64 (Termux专用)"
echo ""

# Windows amd64
build "windows" "amd64" "tlink-windows-amd64.exe"

# Windows arm64
build "windows" "arm64" "tlink-windows-arm64.exe"

echo "=========================================="
echo "编译完成！"
echo "输出目录: $OUTPUT_DIR"
echo ""
ls -lh "$OUTPUT_DIR"
echo ""
echo "📱 Termux 使用说明:"
echo "   将 tlink-termux-arm64 复制到手机"
echo "   在 Termux 中运行: chmod +x tlink-termux-arm64"
echo "   然后运行: ./tlink-termux-arm64"
echo "=========================================="
