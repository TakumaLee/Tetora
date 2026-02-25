# Tetora のインストール

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.zh-CN.md">简体中文</a> | <strong>日本語</strong> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## システム要件

| 要件 | 詳細 |
|---|---|
| **オペレーティングシステム** | macOS、Linux、または Windows（WSL） |
| **ターミナル** | 任意のターミナルエミュレーター |
| **sqlite3** | `PATH` で実行可能であること |
| **AI プロバイダー** | 下記のパス 1 またはパス 2 を参照 |

### sqlite3 のインストール

**macOS：**
```bash
brew install sqlite3
```

**Ubuntu / Debian：**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL：**
```bash
sudo dnf install sqlite
```

**Windows（WSL）：** WSL ディストリビューション内で上記の Linux コマンドを使用してインストールしてください。

---

## Tetora のダウンロード

[Releases ページ](https://github.com/TakumaLee/Tetora/releases/latest) からお使いのプラットフォーム向けバイナリをダウンロードしてください：

| プラットフォーム | ファイル名 |
|---|---|
| macOS（Apple Silicon） | `tetora-darwin-arm64` |
| macOS（Intel） | `tetora-darwin-amd64` |
| Linux（x86_64） | `tetora-linux-amd64` |
| Linux（ARM64） | `tetora-linux-arm64` |
| Windows（WSL） | WSL 内で Linux バイナリを使用 |

**バイナリのインストール：**
```bash
# ダウンロードしたファイル名に置き換えてください
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# ~/.tetora/bin が PATH に含まれていることを確認
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # または ~/.bashrc
source ~/.zshrc
```

**または、ワンライナーインストーラーを使用（macOS / Linux）：**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## パス 1：Claude Pro（月額 $20）— 初心者向け推奨

このパスでは **Claude Code CLI** を AI バックエンドとして使用します。Claude Pro サブスクリプション（月額 $20、[claude.ai](https://claude.ai)）が必要です。

> **このパスを選ぶ理由：** API キーの管理不要、使用量の請求を心配する必要がありません。Pro サブスクリプションで Claude Code を通じた Tetora の使用がすべてカバーされます。

### ステップ 1：Claude Code CLI のインストール

```bash
npm install -g @anthropic-ai/claude-code
```

Node.js がインストールされていない場合：
- **macOS：** `brew install node`
- **Linux：** `sudo apt install nodejs npm`（Ubuntu/Debian）

インストールの確認：
```bash
claude --version
```

Claude Pro アカウントでサインイン：
```bash
claude
# ブラウザベースのログインフローに従ってください
```

### ステップ 2：tetora init の実行

```bash
tetora init
```

セットアップウィザードが次の手順を案内します：
1. **言語の選択** — 希望する言語を選択
2. **メッセージングチャンネルの選択** — Telegram、Discord、Slack、またはなし
3. **AI プロバイダーの選択** — **「Claude Code CLI」** を選択
   - ウィザードが `claude` バイナリの場所を自動検出します
   - Enter を押して検出されたパスを受け入れます
4. **ディレクトリアクセスの選択** — Tetora が読み書きできるフォルダー
5. **最初のエージェントロールの作成** — 名前と個性を設定

### ステップ 3：確認と起動

```bash
# 設定が正しいか確認
tetora doctor

# デーモンを起動
tetora serve
```

Web ダッシュボードを開く：
```bash
tetora dashboard
```

---

## パス 2：API キー

このパスでは API キーを直接使用します。対応プロバイダー：

- **Claude API**（Anthropic）— [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **OpenAI 互換エンドポイント** — Ollama、LM Studio、Azure OpenAI など

> **費用に関する注意：** API の使用量はトークンごとに課金されます。有効にする前にプロバイダーの料金体系をご確認ください。

### ステップ 1：API キーの取得

**Claude API：**
1. [console.anthropic.com](https://console.anthropic.com) にアクセス
2. アカウントを作成またはサインイン
3. **API Keys** → **Create Key** に移動
4. キーをコピー（形式：`sk-ant-...`）

**OpenAI：**
1. [platform.openai.com/api-keys](https://platform.openai.com/api-keys) にアクセス
2. **Create new secret key** をクリック
3. キーをコピー（形式：`sk-...`）

**OpenAI 互換エンドポイント（例：Ollama）：**
```bash
# ローカル Ollama サーバーを起動
ollama serve
# デフォルトエンドポイント：http://localhost:11434/v1
# ローカルモデルには API キーは不要
```

### ステップ 2：tetora init の実行

```bash
tetora init
```

セットアップウィザードが案内します：
1. **言語の選択**
2. **メッセージングチャンネルの選択**
3. **AI プロバイダーの選択：**
   - Anthropic Claude には **「Claude API Key」** を選択
   - OpenAI またはローカルモデルには **「OpenAI 互換エンドポイント」** を選択
4. **API キーの入力**（またはローカルモデルのエンドポイント URL）
5. **ディレクトリアクセスの選択**
6. **最初のエージェントロールの作成**

### ステップ 3：確認と起動

```bash
tetora doctor
tetora serve
```

---

## Web セットアップウィザード（エンジニア以外向け）

グラフィカルなセットアップ画面をご希望の場合は、Web ウィザードをご利用ください：

```bash
tetora setup --web
```

`http://localhost:7474` でブラウザが開き、4 ステップのセットアップウィザードが表示されます。

---

## インストール後によく使うコマンド

| コマンド | 説明 |
|---|---|
| `tetora doctor` | ヘルスチェック — 問題が発生した際に最初に実行 |
| `tetora serve` | デーモンを起動（ボット + HTTP API + スケジュールジョブ） |
| `tetora dashboard` | Web ダッシュボードを開く |
| `tetora status` | クイックステータス表示 |
| `tetora init` | セットアップウィザードを再実行 |

---

## トラブルシューティング

### `tetora: command not found`

`~/.tetora/bin` が PATH に含まれていることを確認：
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

上記「システム要件」に従って sqlite3 をインストールしてください。

### `tetora doctor` でプロバイダーエラーが報告される

- **Claude Code CLI パス：** `which claude` を実行し、`~/.tetora/config.json` の `claudePath` を更新
- **API キーが無効：** プロバイダーのコンソールでキーを確認
- **モデルが見つからない：** モデル名がサブスクリプションプランと一致しているか確認

### Claude Code のログイン問題

```bash
claude logout
claude
```

---

## ソースからのビルド

Go 1.25+ が必要：

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## 次のステップ

- 全機能については [README](README.ja.md) をご覧ください
- コミュニティ：[github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- 問題の報告：[github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
