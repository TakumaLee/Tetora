---
title: "Référence de configuration"
lang: "fr"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Référence de configuration

## Vue d'ensemble

Tetora est configuré par un unique fichier JSON situé à `~/.tetora/config.json`.

**Comportements clés :**

- **Substitution `$ENV_VAR`** — toute valeur de type chaîne commençant par `$` est remplacée par la variable d'environnement correspondante au démarrage. Utilisez ce mécanisme pour les secrets (clés API, tokens) plutôt que de les coder en dur.
- **Hot-reload** — envoyer `SIGHUP` au daemon recharge la configuration. Une configuration invalide sera rejetée et la configuration en cours sera conservée ; le daemon ne plantera pas.
- **Chemins relatifs** — `jobsFile`, `historyDB`, `defaultWorkdir` et les champs de répertoires sont résolus relativement au répertoire du fichier de configuration (`~/.tetora/`).
- **Compatibilité ascendante** — l'ancienne clé `"roles"` est un alias pour `"agents"`. L'ancienne clé `"defaultRole"` dans `smartDispatch` est un alias pour `"defaultAgent"`.

---

## Champs de premier niveau

### Paramètres principaux

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | Adresse d'écoute HTTP pour l'API et le dashboard. Format : `host:port`. |
| `apiToken` | string | `""` | Token Bearer requis pour toutes les requêtes API. Vide signifie pas d'authentification (déconseillé en production). Supporte `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Nombre maximum de tâches d'agent simultanées. Des valeurs supérieures à 20 génèrent un avertissement au démarrage. |
| `defaultModel` | string | `"sonnet"` | Nom du modèle Claude par défaut. Transmis au provider sauf surcharge par agent. |
| `defaultTimeout` | string | `"1h"` | Timeout de tâche par défaut. Format de durée Go : `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Budget de coût par défaut par tâche en USD. `0` signifie sans limite. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Mode de permission Claude par défaut. Valeurs courantes : `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | Agent de repli global lorsqu'aucune règle de routage ne correspond. |
| `defaultWorkdir` | string | `""` | Répertoire de travail par défaut pour les tâches d'agent. Doit exister sur le disque. |
| `claudePath` | string | `"claude"` | Chemin vers le binaire CLI `claude`. Par défaut, recherche `claude` dans `$PATH`. |
| `defaultProvider` | string | `"claude"` | Nom du provider à utiliser quand aucune surcharge au niveau agent n'est définie. |
| `log` | bool | `false` | Indicateur hérité pour activer la journalisation fichier. Préférez `logging.level`. |
| `maxPromptLen` | int | `102400` | Longueur maximale du prompt en octets (100 Ko). Les requêtes dépassant ce seuil sont rejetées. |
| `configVersion` | int | `0` | Version du schéma de configuration. Utilisé pour la migration automatique. Ne pas définir manuellement. |
| `encryptionKey` | string | `""` | Clé AES pour le chiffrement au niveau des champs pour les données sensibles. Supporte `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | Diffuse le statut des tâches en temps réel vers les canaux de messagerie connectés (Discord, Telegram, etc.). |
| `cronNotify` | bool\|null | `null` (true) | `false` supprime toutes les notifications de fin de tâche cron. `null` ou `true` les active. |
| `cronReplayHours` | int | `2` | Nombre d'heures à remonter pour les tâches cron manquées au démarrage du daemon. |
| `diskBudgetGB` | float64 | `1.0` | Espace disque libre minimum en Go. Les tâches cron sont refusées en dessous de ce niveau. |
| `diskWarnMB` | int | `500` | Seuil d'avertissement d'espace disque libre en Mo. Journalise un WARN mais les tâches continuent. |
| `diskBlockMB` | int | `200` | Seuil de blocage d'espace disque libre en Mo. Les tâches sont ignorées avec le statut `skipped_disk_full`. |

### Surcharges de répertoires

Par défaut, tous les répertoires se trouvent sous `~/.tetora/`. Ne surchargez que si vous avez besoin d'une organisation non standard.

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Répertoire pour les fichiers de connaissance du workspace. |
| `agentsDir` | string | `~/.tetora/agents/` | Répertoire contenant les fichiers SOUL.md par agent. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Répertoire pour les règles, la mémoire, les compétences, les brouillons, etc. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Répertoire pour les sessions, sorties, logs, cache. |
| `vaultDir` | string | `~/.tetora/vault/` | Répertoire pour le coffre de secrets chiffrés. |
| `historyDB` | string | `history.db` | Chemin de la base de données SQLite pour l'historique des tâches. Relatif au répertoire de configuration. |
| `jobsFile` | string | `jobs.json` | Chemin vers le fichier de définition des tâches cron. Relatif au répertoire de configuration. |

### Répertoires autorisés globalement

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Champ | Type | Description |
|---|---|---|
| `allowedDirs` | string[] | Répertoires que l'agent est autorisé à lire et écrire. Appliqué globalement ; peut être restreint par agent. |
| `defaultAddDirs` | string[] | Répertoires injectés comme `--add-dir` pour chaque tâche (contexte en lecture seule). |
| `allowedIPs` | string[] | Adresses IP ou plages CIDR autorisées à appeler l'API. Vide = tout autoriser. Exemple : `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Providers

Les providers définissent comment Tetora exécute les tâches d'agent. Plusieurs providers peuvent être configurés et sélectionnés par agent.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `type` | string | requis | Type de provider. L'un de : `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Chemin du binaire. Utilisé par les types `claude-cli` et `claude-code`. Repli sur `claudePath` si vide. |
| `baseUrl` | string | `""` | URL de base de l'API. Requis pour `openai-compatible`. |
| `apiKey` | string | `""` | Clé API. Supporte `$ENV_VAR`. Requis pour `claude-api` ; optionnel pour `openai-compatible`. |
| `model` | string | `""` | Modèle par défaut pour ce provider. Surcharge `defaultModel` pour les tâches utilisant ce provider. |
| `maxTokens` | int | `8192` | Nombre maximum de tokens en sortie (utilisé par `claude-api`). |
| `firstTokenTimeout` | string | `"60s"` | Durée d'attente du premier token de réponse avant expiration (flux SSE). |

**Types de provider :**
- `claude-cli` — exécute le binaire `claude` comme sous-processus (par défaut, plus compatible)
- `claude-api` — appelle l'API Anthropic directement via HTTP (nécessite `ANTHROPIC_API_KEY`)
- `openai-compatible` — toute API REST compatible OpenAI (OpenAI, Ollama, Groq, etc.)
- `claude-code` — utilise le mode CLI de Claude Code

---

## Agents

Les agents définissent des personas nommés avec leur propre modèle, fichier soul et accès aux outils.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `soulFile` | string | requis | Chemin vers le fichier de personnalité SOUL.md de l'agent, relatif à `agentsDir`. |
| `model` | string | `defaultModel` | Modèle à utiliser pour cet agent. |
| `description` | string | `""` | Description lisible par l'humain. Également utilisée par le classifieur LLM pour le routage. |
| `keywords` | string[] | `[]` | Mots-clés qui déclenchent le routage vers cet agent dans le smart dispatch. |
| `provider` | string | `defaultProvider` | Nom du provider (clé dans la map `providers`). |
| `permissionMode` | string | `defaultPermissionMode` | Mode de permission Claude pour cet agent. |
| `allowedDirs` | string[] | `allowedDirs` | Chemins du système de fichiers accessibles par cet agent. Surcharge le paramètre global. |
| `docker` | bool\|null | `null` | Surcharge du bac à sable Docker par agent. `null` = hérite de `docker.enabled` global. |
| `fallbackProviders` | string[] | `[]` | Liste ordonnée de providers de repli si le provider principal échoue. |
| `trustLevel` | string | `"auto"` | Niveau de confiance : `"observe"` (lecture seule), `"suggest"` (proposer sans appliquer), `"auto"` (autonomie complète). |
| `tools` | AgentToolPolicy | `{}` | Politique d'accès aux outils. Voir [Tool Policy](#tool-policy). |
| `toolProfile` | string | `"standard"` | Profil d'outils nommé : `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Paramètres d'isolation du workspace. |

---

## Smart Dispatch

Le Smart Dispatch route automatiquement les tâches entrantes vers l'agent le plus approprié en fonction de règles, de mots-clés et d'une classification LLM.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le routage smart dispatch. |
| `coordinator` | string | premier agent | Agent utilisé pour la classification de tâches par LLM. |
| `defaultAgent` | string | premier agent | Agent de repli quand aucune règle ne correspond. |
| `classifyBudget` | float64 | `0.1` | Budget de coût (USD) pour l'appel LLM de classification. |
| `classifyTimeout` | string | `"30s"` | Timeout pour l'appel de classification. |
| `review` | bool | `false` | Exécute un agent de révision sur la sortie après l'achèvement de la tâche. |
| `reviewLoop` | bool | `false` | Active la boucle Dev↔QA : révision → retour → nouvelle tentative (jusqu'à `maxRetries`). |
| `maxRetries` | int | `3` | Nombre maximum de tentatives QA dans la boucle de révision. |
| `reviewAgent` | string | coordinator | Agent responsable de la révision des sorties. Utilisez un agent QA strict pour une révision adversariale. |
| `reviewBudget` | float64 | `0.2` | Budget de coût (USD) pour l'appel LLM de révision. |
| `fallback` | string | `"smart"` | Stratégie de repli : `"smart"` (routage LLM) ou `"coordinator"` (toujours l'agent par défaut). |
| `rules` | RoutingRule[] | `[]` | Règles de routage par mots-clés/regex évaluées avant la classification LLM. |
| `bindings` | RoutingBinding[] | `[]` | Liaisons canal/utilisateur/guilde → agent (priorité la plus haute, évaluées en premier). |

### `rules` — `RoutingRule`

| Champ | Type | Description |
|---|---|---|
| `agent` | string | Nom de l'agent cible. |
| `keywords` | string[] | Mots-clés insensibles à la casse. Toute correspondance route vers cet agent. |
| `patterns` | string[] | Patterns regex Go. Toute correspondance route vers cet agent. |

### `bindings` — `RoutingBinding`

| Champ | Type | Description |
|---|---|---|
| `channel` | string | Plateforme : `"telegram"`, `"discord"`, `"slack"`, etc. |
| `userId` | string | ID utilisateur sur cette plateforme. |
| `channelId` | string | ID du canal ou du chat. |
| `guildId` | string | ID de la guilde/serveur (Discord uniquement). |
| `agent` | string | Nom de l'agent cible. |

---

## Session

Contrôle la façon dont le contexte de conversation est maintenu et compressé dans les interactions multi-tours.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `contextMessages` | int | `20` | Nombre maximum de messages récents à injecter comme contexte dans une nouvelle tâche. |
| `compactAfter` | int | `30` | Compresse quand le nombre de messages dépasse ce seuil. Déprécié : utilisez `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Conserve les N derniers messages après compression. Déprécié : utilisez `compaction.compactTo`. |
| `compactTokens` | int | `200000` | Compresse quand le total des tokens en entrée dépasse ce seuil. |

### `session.compaction` — `CompactionConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active la compression automatique de session. |
| `maxMessages` | int | `50` | Déclenche la compression quand la session dépasse ce nombre de messages. |
| `compactTo` | int | `10` | Nombre de messages récents à conserver après compression. |
| `model` | string | `"haiku"` | Modèle LLM à utiliser pour générer le résumé de compression. |
| `maxCost` | float64 | `0.02` | Coût maximum par appel de compression (USD). |
| `provider` | string | `defaultProvider` | Provider à utiliser pour l'appel de résumé de compression. |

---

## Task Board

Le tableau de tâches intégré suit les éléments de travail et peut les dispatcher automatiquement vers les agents.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le tableau de tâches. |
| `maxRetries` | int | `3` | Nombre maximum de tentatives par tâche avant d'être marquée comme échouée. |
| `requireReview` | bool | `false` | Contrôle qualité : la tâche doit passer la révision avant d'être marquée terminée. |
| `defaultWorkflow` | string | `""` | Nom du workflow à exécuter pour toutes les tâches auto-dispatchées. Vide = pas de workflow. |
| `gitCommit` | bool | `false` | Commit automatique quand une tâche est marquée terminée. |
| `gitPush` | bool | `false` | Push automatique après le commit (nécessite `gitCommit: true`). |
| `gitPR` | bool | `false` | Création automatique d'une PR GitHub après le push (nécessite `gitPush: true`). |
| `gitWorktree` | bool | `false` | Utilise les worktrees git pour l'isolation des tâches (élimine les conflits de fichiers entre tâches concurrentes). |
| `idleAnalyze` | bool | `false` | Exécute automatiquement une analyse quand le tableau est inactif. |
| `problemScan` | bool | `false` | Analyse la sortie des tâches pour détecter des problèmes latents après l'achèvement. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le sondage automatique et le dispatch des tâches en file d'attente. |
| `interval` | string | `"5m"` | Fréquence de recherche des tâches prêtes. |
| `maxConcurrentTasks` | int | `3` | Nombre maximum de tâches dispatchées par cycle de scan. |
| `defaultModel` | string | `""` | Surcharge du modèle pour les tâches auto-dispatchées. |
| `maxBudget` | float64 | `0` | Coût maximum par tâche (USD). `0` = sans limite. |
| `defaultAgent` | string | `""` | Agent de repli pour les tâches non assignées. |
| `backlogAgent` | string | `""` | Agent pour le triage du backlog. |
| `reviewAgent` | string | `""` | Agent pour la révision des tâches terminées. |
| `escalateAssignee` | string | `""` | Assigne les tâches rejetées à la révision à cet utilisateur. |
| `stuckThreshold` | string | `"2h"` | Les tâches en statut "doing" plus longtemps que ce seuil sont remises en "todo". |
| `backlogTriageInterval` | string | `"1h"` | Fréquence d'exécution du triage du backlog. |
| `reviewLoop` | bool | `false` | Active la boucle Dev↔QA automatisée pour les tâches dispatchées. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Template de nommage des branches. Variables : `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Préfixes de type de branche autorisés. |
| `defaultType` | string | `"feat"` | Type de repli quand aucun n'est spécifié. |
| `autoMerge` | bool | `false` | Fusion automatique vers main quand la tâche est terminée (uniquement avec `gitWorktree: true`). |

---

## Slot Pressure

Contrôle le comportement du système lorsqu'il approche de la limite de slots `maxConcurrent`. Les sessions interactives (initiées par l'humain) obtiennent des slots réservés ; les tâches en arrière-plan attendent.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active la gestion de la pression de slots. |
| `reservedSlots` | int | `2` | Slots réservés pour les sessions interactives. Les tâches en arrière-plan ne peuvent pas les utiliser. |
| `warnThreshold` | int | `3` | Avertit l'utilisateur quand moins de slots que ce nombre sont disponibles. |
| `nonInteractiveTimeout` | string | `"5m"` | Durée pendant laquelle une tâche en arrière-plan attend un slot avant expiration. |
| `pollInterval` | string | `"2s"` | Fréquence à laquelle les tâches en arrière-plan vérifient la disponibilité d'un slot. |
| `monitorEnabled` | bool | `false` | Active les alertes proactives de pression de slots via les canaux de notification. |
| `monitorInterval` | string | `"30s"` | Fréquence de vérification et d'émission des alertes de pression. |

---

## Workflows

Les workflows sont définis comme des fichiers YAML dans un répertoire. `workflowDir` pointe vers ce répertoire ; les variables fournissent les valeurs de template par défaut.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Répertoire où sont stockés les fichiers YAML de workflow. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Déclencheurs automatiques de workflow sur les événements système. |

---

## Intégrations

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le bot Telegram. |
| `botToken` | string | `""` | Token du bot Telegram fourni par @BotFather. Supporte `$ENV_VAR`. |
| `chatID` | int64 | `0` | ID du chat ou groupe Telegram pour l'envoi des notifications. |
| `pollTimeout` | int | `30` | Timeout du long-polling en secondes pour la réception des messages. |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le bot Discord. |
| `botToken` | string | `""` | Token du bot Discord. Supporte `$ENV_VAR`. |
| `guildID` | string | `""` | Restreint à un serveur Discord spécifique (guilde). |
| `channelIDs` | string[] | `[]` | IDs de canaux où le bot répond à tous les messages (mention `@` non requise). |
| `mentionChannelIDs` | string[] | `[]` | IDs de canaux où le bot répond uniquement quand il est `@`-mentionné. |
| `notifyChannelID` | string | `""` | Canal pour les notifications de fin de tâche (crée un thread par tâche). |
| `showProgress` | bool | `true` | Affiche les messages de streaming "En cours..." dans Discord. |
| `webhooks` | map[string]string | `{}` | URLs de webhook nommées pour les notifications sortantes uniquement. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Map d'ID de canal vers nom d'agent pour le routage par canal. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le bot Slack. |
| `botToken` | string | `""` | Token OAuth du bot Slack (`xoxb-...`). Supporte `$ENV_VAR`. |
| `signingSecret` | string | `""` | Secret de signature Slack pour la vérification des requêtes. Supporte `$ENV_VAR`. |
| `appToken` | string | `""` | Token de niveau application Slack pour le mode Socket (`xapp-...`). Optionnel. Supporte `$ENV_VAR`. |
| `defaultChannel` | string | `""` | ID du canal par défaut pour les notifications sortantes. |

### Webhooks sortants

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `url` | string | requis | URL du point de terminaison webhook. |
| `headers` | map[string]string | `{}` | En-têtes HTTP à inclure. Les valeurs supportent `$ENV_VAR`. |
| `events` | string[] | tous | Événements à envoyer : `"success"`, `"error"`, `"timeout"`, `"all"`. Vide = tous. |

### Webhooks entrants

Les webhooks entrants permettent aux services externes de déclencher des tâches Tetora via HTTP POST.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Canaux de notification

Canaux de notification nommés pour router les événements de tâches vers différents points de terminaison Slack/Discord.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `name` | string | `""` | Référence nommée utilisée dans le champ `channel` des tâches (ex. `"discord:alerts"`). |
| `type` | string | requis | `"slack"` ou `"discord"`. |
| `webhookUrl` | string | requis | URL du webhook. Supporte `$ENV_VAR`. |
| `events` | string[] | tous | Filtre par type d'événement : `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | tous | Priorité minimale : `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Marketplace de templates)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le store de templates. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | URL du registre distant pour parcourir et installer les templates. |
| `authToken` | string | `""` | Token d'authentification pour le registre. Supporte `$ENV_VAR`. |

---

## Coûts et alertes

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Limite de dépense journalière en USD. `0` = sans limite. |
| `weeklyLimit` | float64 | `0` | Limite de dépense hebdomadaire en USD. `0` = sans limite. |
| `dailyTokenLimit` | int | `0` | Plafond total de tokens quotidiens (entrée + sortie). `0` = sans plafond. |
| `action` | string | `"warn"` | Action en cas de dépassement : `"warn"` (journaliser et notifier) ou `"pause"` (bloquer les nouvelles tâches). |

### `estimate` — `EstimateConfig`

Estimation du coût avant exécution d'une tâche.

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Demande confirmation quand le coût estimé dépasse cette valeur en USD. |
| `defaultOutputTokens` | int | `500` | Estimation de tokens en sortie de repli quand l'utilisation réelle est inconnue. |

### `budgets` — `BudgetConfig`

Budgets de coût au niveau agent et équipe.

---

## Journalisation

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `level` | string | `"info"` | Niveau de log : `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Format de log : `"text"` (lisible par l'humain) ou `"json"` (structuré). |
| `file` | string | `runtime/logs/tetora.log` | Chemin du fichier de log. Relatif au répertoire runtime. |
| `maxSizeMB` | int | `50` | Taille maximale du fichier de log en Mo avant rotation. |
| `maxFiles` | int | `5` | Nombre de fichiers de log rotatifs à conserver. |

---

## Sécurité

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active l'authentification HTTP Basic sur le dashboard. |
| `username` | string | `"admin"` | Nom d'utilisateur pour l'authentification basic. |
| `password` | string | `""` | Mot de passe pour l'authentification basic. Supporte `$ENV_VAR`. |
| `token` | string | `""` | Alternative : token statique transmis comme cookie. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Champ | Type | Description |
|---|---|---|
| `certFile` | string | Chemin vers le fichier PEM du certificat TLS. Active HTTPS quand défini (avec `keyFile`). |
| `keyFile` | string | Chemin vers le fichier PEM de la clé privée TLS. |

### `rateLimit` — `RateLimitConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active la limitation de débit des requêtes par IP. |
| `maxPerMin` | int | `60` | Nombre maximum de requêtes API par minute par IP. |

### `securityAlert` — `SecurityAlertConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active les alertes de sécurité en cas d'échecs d'authentification répétés. |
| `failThreshold` | int | `10` | Nombre d'échecs dans la fenêtre avant déclenchement d'une alerte. |
| `failWindowMin` | int | `5` | Fenêtre glissante en minutes. |

### `approvalGates` — `ApprovalGateConfig`

Exige une approbation humaine avant l'exécution de certains outils.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active les portes d'approbation. |
| `timeout` | int | `120` | Secondes d'attente d'approbation avant annulation. |
| `tools` | string[] | `[]` | Noms d'outils nécessitant une approbation avant exécution. |
| `autoApproveTools` | string[] | `[]` | Outils pré-approuvés au démarrage (jamais de demande). |

---

## Fiabilité

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Active le disjoncteur pour le basculement de provider. |
| `failThreshold` | int | `5` | Échecs consécutifs avant ouverture du circuit. |
| `successThreshold` | int | `2` | Succès en état semi-ouvert avant fermeture. |
| `openTimeout` | string | `"30s"` | Durée en état ouvert avant nouvelle tentative (semi-ouvert). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Liste ordonnée globale de providers de repli si le provider par défaut échoue.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active la surveillance du heartbeat des agents. |
| `interval` | string | `"30s"` | Fréquence de vérification des tâches en cours pour détecter les blocages. |
| `stallThreshold` | string | `"5m"` | Aucune sortie pendant cette durée = tâche bloquée. |
| `timeoutWarnRatio` | float64 | `0.8` | Avertit quand le temps écoulé dépasse ce ratio du timeout de la tâche. |
| `autoCancel` | bool | `false` | Annule automatiquement les tâches bloquées plus longtemps que `2x stallThreshold`. |
| `notifyOnStall` | bool | `true` | Envoie une notification quand une tâche est détectée comme bloquée. |

### `retention` — `RetentionConfig`

Contrôle le nettoyage automatique des anciennes données.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `history` | int | `90` | Jours de conservation de l'historique des exécutions de tâches. |
| `sessions` | int | `30` | Jours de conservation des données de session. |
| `auditLog` | int | `365` | Jours de conservation des entrées du journal d'audit. |
| `logs` | int | `14` | Jours de conservation des fichiers de log. |
| `workflows` | int | `90` | Jours de conservation des enregistrements d'exécution de workflow. |
| `reflections` | int | `60` | Jours de conservation des enregistrements de réflexion. |
| `sla` | int | `90` | Jours de conservation des enregistrements de vérification SLA. |
| `trustEvents` | int | `90` | Jours de conservation des enregistrements d'événements de confiance. |
| `handoffs` | int | `60` | Jours de conservation des enregistrements de passation/message d'agent. |
| `queue` | int | `7` | Jours de conservation des éléments de file d'attente hors ligne. |
| `versions` | int | `180` | Jours de conservation des instantanés de version de configuration. |
| `outputs` | int | `30` | Jours de conservation des fichiers de sortie d'agent. |
| `uploads` | int | `7` | Jours de conservation des fichiers téléversés. |
| `memory` | int | `30` | Jours avant archivage des entrées de mémoire obsolètes. |
| `claudeSessions` | int | `3` | Jours de conservation des artefacts de session Claude CLI. |
| `piiPatterns` | string[] | `[]` | Patterns regex pour la rédaction des données personnelles dans le contenu stocké. |

---

## Heures silencieuses et digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active les heures silencieuses. Les notifications sont supprimées pendant cette plage. |
| `start` | string | `""` | Début de la période silencieuse (heure locale, format `"HH:MM"`). |
| `end` | string | `""` | Fin de la période silencieuse (heure locale). |
| `tz` | string | locale | Fuseau horaire, ex. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Envoie un digest des notifications supprimées à la fin des heures silencieuses. |

### `digest` — `DigestConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active le digest quotidien planifié. |
| `time` | string | `"08:00"` | Heure d'envoi du digest (`"HH:MM"`). |
| `tz` | string | locale | Fuseau horaire. |

---

## Outils

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `maxIterations` | int | `10` | Nombre maximum d'itérations d'appels d'outils par tâche. |
| `timeout` | int | `120` | Timeout global du moteur d'outils en secondes. |
| `toolOutputLimit` | int | `10240` | Nombre maximum de caractères par sortie d'outil (tronqué au-delà). |
| `toolTimeout` | int | `30` | Timeout d'exécution par outil en secondes. |
| `defaultProfile` | string | `"standard"` | Nom du profil d'outils par défaut. |
| `builtin` | map[string]bool | `{}` | Active/désactive les outils intégrés individuels par nom. |
| `profiles` | map[string]ToolProfile | `{}` | Profils d'outils personnalisés. |
| `trustOverride` | map[string]string | `{}` | Surcharge du niveau de confiance par nom d'outil. |

### `tools.webSearch` — `WebSearchConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `provider` | string | `""` | Provider de recherche : `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | Clé API pour le provider. Supporte `$ENV_VAR`. |
| `baseURL` | string | `""` | Point de terminaison personnalisé (pour searxng auto-hébergé). |
| `maxResults` | int | `5` | Nombre maximum de résultats de recherche à retourner. |

### `tools.vision` — `VisionConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `provider` | string | `""` | Provider vision : `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | Clé API. Supporte `$ENV_VAR`. |
| `model` | string | `""` | Nom du modèle pour le provider vision. |
| `maxImageSize` | int | `5242880` | Taille maximale d'image en octets (5 Mo par défaut). |
| `baseURL` | string | `""` | Point de terminaison API personnalisé. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Configurations de serveur MCP nommées. Chaque clé est un nom de configuration MCP ; la valeur est la configuration JSON MCP complète. Tetora écrit ces fichiers dans des fichiers temporaires et les transmet au binaire claude via `--mcp-config`.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Définitions simplifiées de serveurs MCP gérées directement par Tetora.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `command` | string | requis | Commande exécutable. |
| `args` | string[] | `[]` | Arguments de la commande. |
| `env` | map[string]string | `{}` | Variables d'environnement pour le processus. Les valeurs supportent `$ENV_VAR`. |
| `enabled` | bool | `true` | Indique si ce serveur MCP est actif. |

---

## Budget de prompt

Contrôle les budgets maximaux de caractères pour chaque section du prompt système. À ajuster quand les prompts sont tronqués de façon inattendue.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `soulMax` | int | `8000` | Nombre maximum de caractères pour le prompt soul/personnalité de l'agent. |
| `rulesMax` | int | `4000` | Nombre maximum de caractères pour les règles du workspace. |
| `knowledgeMax` | int | `8000` | Nombre maximum de caractères pour le contenu de la base de connaissances. |
| `skillsMax` | int | `4000` | Nombre maximum de caractères pour les compétences injectées. |
| `maxSkillsPerTask` | int | `3` | Nombre maximum de compétences injectées par tâche. |
| `contextMax` | int | `16000` | Nombre maximum de caractères pour le contexte de session. |
| `totalMax` | int | `40000` | Plafond strict sur la taille totale du prompt système (toutes sections combinées). |

---

## Communication entre agents

Contrôle le dispatch imbriqué de sous-agents (outil agent_dispatch).

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Champ | Type | Défaut | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Active l'outil `agent_dispatch` pour les appels de sous-agents imbriqués. |
| `maxConcurrent` | int | `3` | Nombre maximum d'appels `agent_dispatch` simultanés globalement. |
| `defaultTimeout` | int | `900` | Timeout par défaut des sous-agents en secondes. |
| `maxDepth` | int | `3` | Profondeur d'imbrication maximale pour les sous-agents. |
| `maxChildrenPerTask` | int | `5` | Nombre maximum d'agents enfants simultanés par tâche parente. |

---

## Exemples

### Configuration minimale

Une configuration minimale pour démarrer avec le provider CLI Claude :

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Configuration multi-agents avec Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Configuration complète (toutes les sections principales)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
