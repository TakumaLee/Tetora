---
title: "Intégration MCP (Model Context Protocol)"
lang: "fr"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# Intégration MCP (Model Context Protocol)

Tetora inclut un serveur MCP intégré qui permet aux agents IA (Claude Code, etc.) d'interagir avec les APIs de Tetora via le protocole MCP standard.

## Architecture

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (processus bridge)            (localhost:8991)
```

Le serveur MCP est un **bridge stdio JSON-RPC 2.0** — il lit les requêtes depuis stdin, les proxie vers l'API HTTP de Tetora, et écrit les réponses sur stdout. Claude Code le lance comme un processus enfant.

## Configuration

### 1. Ajouter le serveur MCP aux paramètres Claude Code

Ajoutez ce qui suit dans `~/.claude/settings.json` :

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

Remplacez le chemin par l'emplacement réel de votre binaire `tetora`. Trouvez-le avec :

```bash
which tetora
# ou
ls ~/.tetora/bin/tetora
```

### 2. S'assurer que le daemon Tetora est en cours d'exécution

Le bridge MCP proxie vers l'API HTTP Tetora, donc le daemon doit être en cours d'exécution :

```bash
tetora start
```

### 3. Vérifier

Redémarrez Claude Code. Les outils MCP apparaîtront comme outils disponibles préfixés par `tetora_`.

## Outils disponibles

### Gestion des tâches

| Outil | Description |
|------|-------------|
| `tetora_taskboard_list` | Liste les tickets du tableau kanban. Filtres optionnels : `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | Met à jour une tâche (statut, responsable, priorité, titre). Nécessite `id`. |
| `tetora_taskboard_comment` | Ajoute un commentaire à une tâche. Nécessite `id` et `comment`. |

### Mémoire

| Outil | Description |
|------|-------------|
| `tetora_memory_get` | Lit une entrée de mémoire. Nécessite `agent` et `key`. |
| `tetora_memory_set` | Écrit une entrée de mémoire. Nécessite `agent`, `key` et `value`. |
| `tetora_memory_search` | Liste toutes les entrées de mémoire. Filtre optionnel : `role`. |

### Dispatch

| Outil | Description |
|------|-------------|
| `tetora_dispatch` | Dispatche une tâche vers un autre agent. Crée une nouvelle session Claude Code. Nécessite `prompt`. Optionnels : `agent`, `workdir`, `model`. |

### Connaissances

| Outil | Description |
|------|-------------|
| `tetora_knowledge_search` | Recherche dans la base de connaissances partagée. Nécessite `q`. Optionnel : `limit`. |

### Notifications

| Outil | Description |
|------|-------------|
| `tetora_notify` | Envoie une notification à l'utilisateur via Discord/Telegram. Nécessite `message`. Optionnel : `level` (info/warn/error). |
| `tetora_ask_user` | Pose une question à l'utilisateur via Discord et attend une réponse (jusqu'à 6 minutes). Nécessite `question`. Optionnel : `options` (boutons de réponse rapide, max 4). |

## Détail des outils

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

Tous les paramètres sont optionnels. Retourne un tableau JSON de tâches.

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

Seul `id` est requis. Les autres champs ne sont mis à jour que s'ils sont fournis. Valeurs de statut : `todo`, `in_progress`, `review`, `done`.

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

Seul `prompt` est requis. Si `agent` est omis, le smart dispatch de Tetora route vers le meilleur agent.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

Il s'agit d'un **appel bloquant** — il attend jusqu'à 6 minutes que l'utilisateur réponde via Discord. L'utilisateur voit la question avec des boutons de réponse rapide optionnels et peut aussi taper une réponse personnalisée.

## Commandes CLI

### Gestion des serveurs MCP externes

Tetora peut également agir comme **hôte** MCP, en se connectant à des serveurs MCP externes :

```bash
# Lister les serveurs MCP configurés
tetora mcp list

# Afficher la configuration complète d'un serveur
tetora mcp show <name>

# Ajouter un nouveau serveur MCP
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Supprimer une configuration de serveur
tetora mcp remove <name>

# Tester la connexion au serveur
tetora mcp test <name>
```

### Exécuter le bridge MCP

```bash
# Démarrer le serveur bridge MCP (normalement lancé par Claude Code, pas manuellement)
tetora mcp-server
```

Au premier lancement, cela génère `~/.tetora/mcp/bridge.json` avec le chemin correct du binaire.

## Configuration

Paramètres MCP dans `config.json` :

| Champ | Type | Défaut | Description |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Map de configurations de serveurs MCP externes (nom → {command, args, env}). |

Le serveur bridge lit `listenAddr` et `apiToken` depuis la configuration principale pour se connecter au daemon.

## Authentification

Si `apiToken` est défini dans `config.json`, le bridge MCP inclut automatiquement `Authorization: Bearer <token>` dans toutes les requêtes HTTP vers le daemon. Aucune authentification supplémentaire au niveau MCP n'est nécessaire.

## Résolution de problèmes

**Les outils n'apparaissent pas dans Claude Code :**
- Vérifiez que le chemin du binaire dans `settings.json` est correct
- Assurez-vous que le daemon Tetora est en cours d'exécution (`tetora start`)
- Consultez les logs Claude Code pour les erreurs de connexion MCP

**Erreurs "HTTP 401" :**
- L'`apiToken` dans `config.json` doit correspondre. Le bridge le lit automatiquement.

**Erreurs "connection refused" :**
- Le daemon n'est pas en cours d'exécution, ou `listenAddr` ne correspond pas. Par défaut : `127.0.0.1:8991`.

**`tetora_ask_user` expire :**
- L'utilisateur dispose de 6 minutes pour répondre via Discord. Assurez-vous que le bot Discord est connecté.
