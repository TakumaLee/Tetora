#!/bin/bash

# ==============================================================================
# Tetora 自主化初始化腳本 (tetora-init.sh)
# 功用：引導新使用者建立專屬主題的 AI 團隊
# ==============================================================================

echo "🚀 歡迎使用 Tetora AI 幕府初始化系統！"
echo "------------------------------------------------"

# 1. 主題定義
read -p "❓ 請輸入您的團隊/組織名稱 (預設: MyCabinet): " CABINET_NAME
CABINET_NAME=${CABINET_NAME:-MyCabinet}

read -p "❓ 請輸入您的主題風格 (例如: 三國, 星際大戰, 現代企業, 奇幻世界): " THEME_STYLE
THEME_STYLE=${THEME_STYLE:-現代企業}

echo "✨ 正在為您建立【$CABINET_NAME】($THEME_STYLE 風格)..."

# 2. 目錄建立
mkdir -p agents workspace/{memory,rules,knowledge,skills,tasks} workflows

# 3. 角色引導 (範例：建立一個核心協調官)
read -p "❓ 請輸入您的第一位 Agent 名字: " AGENT_NAME
read -p "❓ 請簡單描述這位 Agent 的性格與職責: " AGENT_DESC

mkdir -p "agents/$AGENT_NAME"
cat > "agents/$AGENT_NAME/SOUL.md" <<EOF
# $AGENT_NAME — $CABINET_NAME 重臣
## 身份與背景
您是處於「$THEME_STYLE」世界觀下的 $AGENT_NAME。
$AGENT_DESC

## 行為準則
- 始終保持符合「$THEME_STYLE」風格的口吻。
- 協助使用者達成目標，並與其他 Agent 協作。
EOF

# 4. 生成通用配置文件
cat > config.json.example <<EOF
{
  "agents": {
    "$AGENT_NAME": {
      "soulFile": "agents/$AGENT_NAME/SOUL.md",
      "model": "sonnet",
      "description": "$AGENT_DESC"
    }
  },
  "defaultAgent": "$AGENT_NAME",
  "taskBoard": {
    "enabled": true,
    "autoDispatch": { "enabled": true, "interval": "5m" }
  },
  "providers": {
    "anthropic": { "apiKey": "\$ANTHROPIC_API_KEY" }
  }
}
EOF

echo "------------------------------------------------"
echo "✅ 初始化完成！"
echo "📂 已建立 agents/ 與 workspace/ 目錄。"
echo "📝 已為您生成第一個 Agent: $AGENT_NAME。"
echo "👉 下一步：將 config.json.example 重新命名為 config.json 並填入金鑰。"
echo "👉 隨後執行 'tetora serve' 啟動您的 $CABINET_NAME！"
