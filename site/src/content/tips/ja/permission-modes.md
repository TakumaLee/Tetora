---
title: "パーミッションモード — エージェントの変更範囲を制御する"
lang: ja
date: "2026-04-28"
excerpt: "コンテンツエージェントがインフラ設定を触れないようにしよう。Tetoraの3つのパーミッションモードでエージェントの書き込みアクセスをロックする方法を解説します。"
description: "Tetoraのパーミッションモード（review、plan、acceptEdits）の実践ガイド。各エージェントが触れるべきファイルだけにアクセスできるよう設定する方法を紹介します。"
---

## 問題

ブログ記事の草稿作成のためにエージェントをディスパッチしました。草稿が完成すると、エージェントは `tetora.config.json` が「整合性がない」と判断し、書き換えてしまいました。その結果、cronジョブがすべて壊れました。

境界のない書き込みアクセスは、小さな「善意」が大きなインシデントになる原因です。

## 3つのパーミッションモード

Tetoraは、エージェントが書き込める範囲を制御する3つのモードを提供しています：

| モード | 読み取り | 書き込み | 使用場面 |
|---|---|---|---|
| `review` | ✅ すべて | ❌ 不可 | 監査、コードレビュー、調査 |
| `plan` | ✅ すべて | ✅ タスク仕様のみ | 計画作成、チケット起票 |
| `acceptEdits` | ✅ すべて | ✅ スコープ内のみ | 実際の実装作業 |

`tetora.config.json` で各エージェントのデフォルトモードを設定します：

```json
{
  "agents": {
    "kohaku": {
      "permission_mode": "acceptEdits",
      "scope": ["site/src/content/**"]
    },
    "hisui": {
      "permission_mode": "review",
      "scope": ["**/*"]
    },
    "tekkou": {
      "permission_mode": "acceptEdits",
      "scope": ["src/**", "tests/**"]
    }
  }
}
```

`kohaku` は `site/src/content/` 以下にのみ書き込めます。`hisui` はどこでも読み取り専用。`tekkou` はソースとテストディレクトリを担当します。

## ディスパッチごとの上書き

設定ファイルを変更せずに、特定のタスクだけエージェントのパーミッションを調整したい場合：

```bash
# hisui を一時的に昇格して一回限りの修正を実行
tetora dispatch --agent hisui --permission acceptEdits --scope "docs/**" \
  "ドキュメント内のリンク切れをすべて修正する"

# 機密監査のために tekkou を review モードに制限
tetora dispatch --agent tekkou --permission review \
  "データベースマイグレーションファイルの正確性を監査する"
```

上書きはそのディスパッチにのみ適用されます。次のタスクは設定ファイルのデフォルト値を使用します。

## スコープ境界の強制

`acceptEdits` のエージェントが宣言された `scope` 外に書き込もうとすると、Tetoraは書き込みをブロックし、スコープ違反としてログに記録します（サイレント失敗ではありません）：

```
[SCOPE VIOLATION] tekkou attempted write to site/src/content/tips/
  Allowed: src/**, tests/**
  Action: BLOCKED — logged to tasks/scope-violations.log
```

サイレントなドリフトなし。事後調査不要。

## まとめ

エージェントに必要最小限のパーミッションを与えましょう。コンテンツエージェントに本番データベースへのアクセス権は不要です。コードレビュアーにコミット権限は不要です。`tetora.config.json` に一度設定するだけで、「エージェントが何を変更したのか？」という事故のクラス全体を予防できます。

次のステップ：**ロールごとのコストガバナンス** で、同じ原則をモデルのコスト管理に適用する方法を学びましょう。
