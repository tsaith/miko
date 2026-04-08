#!/bin/bash

# 使用 pkill 刪除匹配完整指令的進程
# -f: 匹配完整的指令列
# -e: 顯示被刪除的進程資訊 (選填)
pkill -f "uv run python -m app.server"
pkill -f "python3 -m app.server"

if [ $? -eq 0 ]; then
    echo "成功刪除相關進程。"
else
    echo "未發現匹配的進程。"
fi
