---
title: "Intégration des hooks Claude Code"
lang: "fr"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Intégration des hooks Claude Code

## Vue d'ensemble

Les hooks Claude Code sont un système d'événements intégré à Claude Code qui déclenche des commandes shell à des moments clés d'une session. Tetora s'enregistre comme récepteur de hooks afin d'observer chaque session d'agent en temps réel — sans polling, sans tmux, et sans injecter de scripts wrapper.

**Ce que les hooks permettent :**

- Suivi de la progression en temps réel dans le dashboard (appels d'outils, état de session, liste des workers en direct)
- Surveillance des coûts et des tokens via le bridge statusline
- Audit des appels d'outils (quels outils ont été exécutés, dans quelle session, dans quel répertoire)
- Détection de la fin de session et mises à jour automatiques du statut des tâches
- Portail plan mode : bloque `ExitPlanMode` jusqu'à ce qu'un humain approuve le plan dans le dashboard
- Routage des questions interactives : `AskUserQuestion` est redirigé vers le bridge MCP afin que les questions apparaissent sur votre plateforme de chat plutôt que de bloquer le terminal

Les hooks sont la méthode d'intégration recommandée depuis Tetora v2.0. L'ancienne approche basée sur tmux (v1.x) fonctionne toujours mais ne prend pas en charge les fonctionnalités exclusives aux hooks comme le portail plan et le routage des questions.

---

## Architecture

```
Session Claude Code
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Portail plan : long-poll jusqu'à approbation
  │   (AskUserQuestion)               └─► Refus : redirection vers le bridge MCP
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Mise à jour de l'état du worker
  │                                   └─► Détection des écritures dans les fichiers de plan
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Marque le worker comme terminé
  │                                   └─► Déclenche la complétion de la tâche
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Transfert vers Discord/Telegram
```

La commande hook est un petit appel curl injecté dans `~/.claude/settings.json` de Claude Code. Chaque événement est posté vers `POST /api/hooks/event` sur le daemon Tetora en cours d'exécution.

---

## Configuration

### Installer les hooks

Avec le daemon Tetora en cours d'exécution :

```bash
tetora hooks install
```

Cette commande écrit des entrées dans `~/.claude/settings.json` et génère la configuration du bridge MCP dans `~/.tetora/mcp/bridge.json`.

Exemple de ce qui est écrit dans `~/.claude/settings.json` :

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### Vérifier le statut

```bash
tetora hooks status
```

La sortie indique quels hooks sont installés, combien de règles Tetora sont enregistrées, et le nombre total d'événements reçus depuis le démarrage du daemon.

Vous pouvez aussi vérifier depuis le dashboard : **Engineering Details → Hooks** affiche le même statut ainsi qu'un flux d'événements en direct.

### Supprimer les hooks

```bash
tetora hooks remove
```

Supprime toutes les entrées Tetora de `~/.claude/settings.json`. Les hooks existants non liés à Tetora sont préservés.

---

## Événements de hook

### PostToolUse

Se déclenche après chaque appel d'outil terminé. Tetora l'utilise pour :

- Suivre les outils utilisés par un agent (`Bash`, `Write`, `Edit`, `Read`, etc.)
- Mettre à jour les champs `lastTool` et `toolCount` du worker dans la liste des workers en direct
- Détecter quand un agent écrit dans un fichier de plan (déclenche une mise à jour du cache de plan)

### Stop

Se déclenche quand une session Claude Code se termine (complétion naturelle ou annulation). Tetora l'utilise pour :

- Marquer le worker comme `done` dans la liste des workers en direct
- Publier un événement SSE de complétion vers le dashboard
- Déclencher les mises à jour de statut des tâches du taskboard en aval

### Notification

Se déclenche quand Claude Code envoie une notification (ex. permission requise, longue pause). Tetora les transfère vers Discord/Telegram et les publie dans le flux SSE du dashboard.

### PreToolUse : ExitPlanMode (portail plan)

Quand un agent est sur le point de quitter le mode plan, Tetora intercepte l'événement avec un long-poll (timeout : 600 secondes). Le contenu du plan est mis en cache et affiché dans le dashboard sous la vue détaillée de la session.

Un humain peut approuver ou rejeter le plan depuis le dashboard. En cas d'approbation, le hook retourne et Claude Code continue. En cas de rejet (ou d'expiration du timeout), la sortie est bloquée et Claude Code reste en mode plan.

### PreToolUse : AskUserQuestion (routage des questions)

Quand Claude Code tente de poser une question à l'utilisateur de façon interactive, Tetora l'intercepte et refuse le comportement par défaut. La question est routée via le bridge MCP, apparaissant sur votre plateforme de chat configurée (Discord, Telegram, etc.) afin que vous puissiez répondre sans rester devant un terminal.

---

## Suivi de la progression en temps réel

Une fois les hooks installés, le panneau **Workers** du dashboard affiche les sessions en direct :

| Champ | Source |
|---|---|
| ID de session | `session_id` dans l'événement hook |
| État | `working` / `idle` / `done` |
| Dernier outil | Nom de l'outil du `PostToolUse` le plus récent |
| Répertoire de travail | `cwd` de l'événement hook |
| Nombre d'appels d'outils | Compteur cumulatif de `PostToolUse` |
| Coût / tokens | Bridge statusline (`POST /api/hooks/usage`) |
| Origine | Tâche ou tâche cron liée si dispatchée par Tetora |

Les données de coût et de tokens proviennent du script statusline Claude Code, qui poste vers `/api/hooks/usage` à un intervalle configurable. Le script statusline est distinct des hooks — il lit la sortie de la barre de statut Claude Code et la transfère à Tetora.

---

## Surveillance des coûts

Le point de terminaison d'utilisation (`POST /api/hooks/usage`) reçoit :

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

Ces données sont visibles dans le panneau Workers du dashboard et sont agrégées dans les graphiques de coûts quotidiens. Les alertes de budget se déclenchent quand le coût d'une session dépasse le budget configuré par rôle ou global.

---

## Résolution de problèmes

### Les hooks ne se déclenchent pas

**Vérifier que le daemon est en cours d'exécution :**
```bash
tetora status
```

**Vérifier que les hooks sont installés :**
```bash
tetora hooks status
```

**Vérifier settings.json directement :**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

Si la clé hooks est manquante, relancez `tetora hooks install`.

**Vérifier que le daemon peut recevoir les événements hook :**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Réponse attendue : {"ok":true}
```

Si le daemon n'écoute pas sur le port attendu, vérifiez `listenAddr` dans `config.json`.

### Erreurs de permissions sur settings.json

Le fichier `settings.json` de Claude Code se trouve à `~/.claude/settings.json`. Si le fichier appartient à un autre utilisateur ou a des permissions restrictives :

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Le panneau workers du dashboard est vide

1. Confirmez que les hooks sont installés et que le daemon est en cours d'exécution.
2. Démarrez une session Claude Code manuellement et exécutez un outil (ex. `ls`).
3. Vérifiez le panneau Workers du dashboard — la session devrait apparaître en quelques secondes.
4. Si ce n'est pas le cas, consultez les logs du daemon : `tetora logs -f | grep hooks`

### Le portail plan n'apparaît pas

Le portail plan ne s'active que quand Claude Code tente d'appeler `ExitPlanMode`. Cela se produit uniquement dans les sessions en mode plan (démarrées avec `--plan` ou définies via `permissionMode: "plan"` dans la configuration du rôle). Les sessions interactives `acceptEdits` n'utilisent pas le mode plan.

### Les questions ne sont pas routées vers le chat

Le hook de refus `AskUserQuestion` nécessite que le bridge MCP soit configuré. Relancez `tetora hooks install` — cela régénère la configuration du bridge. Ajoutez ensuite le bridge à vos paramètres MCP de Claude Code :

```bash
cat ~/.tetora/mcp/bridge.json
```

Ajoutez ce fichier comme serveur MCP dans `~/.claude/settings.json` sous `mcpServers`.

---

## Migration depuis tmux (v1.x)

Dans Tetora v1.x, les agents s'exécutaient dans des panneaux tmux et Tetora les surveillait en lisant la sortie des panneaux. Dans v2.0, les agents s'exécutent comme des processus Claude Code nus et Tetora les observe via les hooks.

**Si vous migrez depuis v1.x :**

1. Exécutez `tetora hooks install` une fois après la mise à jour.
2. Supprimez toute configuration de gestion de session tmux de `config.json` (les clés `tmux.*` sont désormais ignorées).
3. L'historique de session existant est préservé dans `history.db` — aucune migration nécessaire.
4. La commande `tetora session list` et l'onglet Sessions du dashboard continuent de fonctionner comme avant.

Le bridge terminal tmux (`discord_terminal.go`) est toujours disponible pour l'accès interactif au terminal via Discord. C'est distinct de l'exécution des agents — il permet d'envoyer des frappes clavier à une session de terminal en cours. Les hooks et le bridge terminal sont complémentaires, non mutuellement exclusifs.
