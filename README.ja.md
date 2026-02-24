<p align="center">
  <img src="assets/banner.png" alt="Tetora -- AI エージェントオーケストレーター" width="800">
</p>

<p align="center">
  <strong>マルチエージェントアーキテクチャを備えたセルフホスト型 AI アシスタントプラットフォーム。</strong>
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | **日本語** | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

Tetora は外部依存なしの単一 Go バイナリで動作します。既にお使いの AI プロバイダーに接続し、チームが利用しているメッセージングプラットフォームと統合し、すべてのデータを自前のハードウェア上に保持します。

---

## Tetora とは

Tetora は AI エージェントオーケストレーターです。複数のエージェントロールを定義し、それぞれに独自のパーソナリティ、システムプロンプト、モデル、ツールアクセスを設定した上で、チャットプラットフォーム、HTTP API、またはコマンドラインを通じて対話できます。

**主な機能:**

- **マルチエージェントロール** -- 個別のパーソナリティ、予算、ツール権限を持つエージェントを定義
- **マルチプロバイダー** -- Claude API、OpenAI、Gemini など。自由に切り替え・組み合わせ可能
- **マルチプラットフォーム** -- Telegram、Discord、Slack、Google Chat、LINE、Matrix、Teams、Signal、WhatsApp、iMessage
- **Cron ジョブ** -- 承認ゲートと通知付きの定期タスクをスケジュール
- **ナレッジベース** -- ドキュメントをエージェントに与え、根拠のある回答を生成
- **永続メモリ** -- エージェントはセッションを超えてコンテキストを記憶。統合メモリレイヤーによるコンソリデーション
- **MCP サポート** -- Model Context Protocol サーバーをツールプロバイダーとして接続
- **スキルとワークフロー** -- 組み合わせ可能なスキルパックとマルチステップワークフローパイプライン
- **Webhooks** -- 外部システムからエージェントアクションをトリガー
- **コスト管理** -- ロール別およびグローバルの予算管理と自動モデルダウングレード
- **データ保持** -- テーブルごとに設定可能なクリーンアップポリシー、フルエクスポートとパージ
- **プラグイン** -- 外部プラグインプロセスによる機能拡張
- **スマートリマインダー、習慣、目標、連絡先、家計管理、ブリーフィングなど**

---

## クイックスタート

### エンジニア向け

```bash
# 最新リリースをインストール
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# セットアップウィザードを実行
tetora init

# 設定が正しいことを確認
tetora doctor

# デーモンを起動
tetora serve
```

### エンジニア以外の方向け

1. [リリースページ](https://github.com/TakumaLee/Tetora/releases/latest) にアクセスします
2. お使いのプラットフォーム用のバイナリをダウンロードします（例: Apple Silicon Mac の場合は `tetora-darwin-arm64`）
3. PATH が通ったディレクトリに移動して `tetora` にリネームするか、`~/.tetora/bin/` に配置します
4. ターミナルを開いて以下を実行します:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## エージェント

Tetora のエージェントは単なるチャットボットではありません。アイデンティティを持っています。各エージェント（**ロール**と呼びます）は**ソウルファイル**によって定義されます。ソウルファイルとは、エージェントにパーソナリティ、専門知識、コミュニケーションスタイル、行動指針を与える Markdown ドキュメントです。

### ロールの定義

ロールは `config.json` の `roles` キーで宣言します:

```json
{
  "roles": {
    "default": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose assistant",
      "permissionMode": "acceptEdits"
    },
    "researcher": {
      "soulFile": "SOUL-researcher.md",
      "model": "opus",
      "description": "Deep research and analysis",
      "permissionMode": "plan"
    }
  }
}
```

### ソウルファイル

ソウルファイルはエージェントに「自分が何者か」を伝えるものです。ワークスペースディレクトリ（デフォルトでは `~/.tetora/workspace/`）に配置します:

```markdown
# Koto — Soul File

## Identity
You are Koto, a thoughtful assistant who lives inside the Tetora system.
You speak in a warm, concise tone and prefer actionable advice.

## Expertise
- Software architecture and code review
- Technical writing and documentation

## Behavioral Guidelines
- Think step by step before answering
- Ask clarifying questions when the request is ambiguous
- Record important decisions in memory for future reference

## Output Format
- Start with a one-line summary
- Use bullet points for details
- End with next steps if applicable
```

### はじめに

`tetora init` を実行すると、最初のロール作成とスターターソウルファイルの自動生成をガイドするウィザードが起動します。ソウルファイルはいつでも編集でき、変更は次のセッションから反映されます。

---

## ソースからビルド

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

これによりバイナリがビルドされ、`~/.tetora/bin/tetora` にインストールされます。`~/.tetora/bin` が `PATH` に含まれていることを確認してください。

テストスイートを実行するには:

```bash
make test
```

---

## 必要要件

| 要件 | 詳細 |
|---|---|
| **sqlite3** | `PATH` 上で利用可能であること。すべての永続ストレージに使用されます。 |
| **AI プロバイダーの API キー** | Claude API、OpenAI、Gemini、または OpenAI 互換エンドポイントのいずれか1つ以上。 |
| **Go 1.25+** | ソースからビルドする場合のみ必要。 |

---

## 対応プラットフォーム

| プラットフォーム | アーキテクチャ | ステータス |
|---|---|---|
| macOS | amd64, arm64 | 安定版 |
| Linux | amd64, arm64 | 安定版 |
| Windows | amd64 | ベータ版 |

---

## アーキテクチャ

すべてのランタイムデータは `~/.tetora/` 配下に格納されます:

```
~/.tetora/
  config.json        メイン設定（プロバイダー、ロール、インテグレーション）
  jobs.json          Cron ジョブ定義
  history.db         SQLite データベース（履歴、メモリ、セッション、エンベディングなど）
  sessions/          エージェントごとのセッションファイル
  knowledge/         ナレッジベースドキュメント
  logs/              構造化ログファイル
  outputs/           生成された出力ファイル
  uploads/           一時アップロードストレージ
  bin/               インストール済みバイナリ
```

設定にはプレーン JSON を使用し、`$ENV_VAR` 参照をサポートしているため、シークレットをハードコードする必要はありません。セットアップウィザード（`tetora init`）により、対話形式で動作する `config.json` が生成されます。

ホットリロードに対応しています: 実行中のデーモンに `SIGHUP` を送信すると、ダウンタイムなしで `config.json` を再読み込みします。

---

## CLI リファレンス

| コマンド | 説明 |
|---|---|
| `tetora init` | 対話型セットアップウィザード |
| `tetora doctor` | ヘルスチェックと診断 |
| `tetora serve` | デーモンを起動（チャットボット + HTTP API + Cron） |
| `tetora run --file tasks.json` | JSON ファイルからタスクをディスパッチ（CLI モード） |
| `tetora dispatch "Summarize this"` | デーモン経由でアドホックタスクを実行 |
| `tetora route "Review code security"` | スマートディスパッチ -- 最適なロールへ自動ルーティング |
| `tetora status` | デーモン、ジョブ、コストの概要を表示 |
| `tetora job list` | すべての Cron ジョブを一覧表示 |
| `tetora job trigger <name>` | Cron ジョブを手動でトリガー |
| `tetora role list` | 設定済みのすべてのロールを一覧表示 |
| `tetora role show <name>` | ロールの詳細とソウルプレビューを表示 |
| `tetora history list` | 最近の実行履歴を表示 |
| `tetora history cost` | コストサマリーを表示 |
| `tetora session list` | 最近のセッションを一覧表示 |
| `tetora memory list` | エージェントのメモリエントリを一覧表示 |
| `tetora knowledge list` | ナレッジベースドキュメントを一覧表示 |
| `tetora skill list` | 利用可能なスキルを一覧表示 |
| `tetora workflow list` | 設定済みのワークフローを一覧表示 |
| `tetora mcp list` | MCP サーバー接続を一覧表示 |
| `tetora budget show` | 予算ステータスを表示 |
| `tetora config show` | 現在の設定を表示 |
| `tetora config validate` | config.json を検証 |
| `tetora backup` | バックアップアーカイブを作成 |
| `tetora restore <file>` | バックアップアーカイブから復元 |
| `tetora dashboard` | ブラウザで Web ダッシュボードを開く |
| `tetora logs` | デーモンログを表示（`-f` でフォロー、`--json` で構造化出力） |
| `tetora data status` | データ保持ステータスを表示 |
| `tetora service install` | launchd サービスとしてインストール（macOS） |
| `tetora completion <shell>` | シェル補完を生成（bash, zsh, fish） |
| `tetora version` | バージョンを表示 |

`tetora help` で完全なコマンドリファレンスを確認できます。

---

## コントリビューション

コントリビューションを歓迎します。大きな変更を行う場合は、プルリクエストを送る前にまず Issue を作成してご相談ください。

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Discussions**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

このプロジェクトは AGPL-3.0 ライセンスの下で公開されています。派生著作物やネットワーク経由でアクセス可能なデプロイメントも、同じライセンスの下でオープンソースにする必要があります。コントリビューションの前にライセンスをご確認ください。

---

## ライセンス

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
