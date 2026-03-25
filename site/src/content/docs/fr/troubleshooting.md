---
title: "Guide de résolution de problèmes"
lang: "fr"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Guide de résolution de problèmes

Ce guide couvre les problèmes les plus courants rencontrés lors de l'utilisation de Tetora. Pour chaque problème, la cause la plus probable est listée en premier.

---

## tetora doctor

Commencez toujours ici. Exécutez `tetora doctor` après l'installation ou quand quelque chose cesse de fonctionner :

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

Chaque ligne est une vérification. Un `✗` rouge indique un échec critique (le daemon ne fonctionnera pas sans correction). Un `~` jaune indique une suggestion (optionnelle mais recommandée).

Corrections courantes pour les vérifications échouées :

| Vérification échouée | Correction |
|---|---|
| `Config: not found` | Exécuter `tetora init` |
| `Claude CLI: not found` | Définir `claudePath` dans `config.json` ou installer Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) ou `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | Créer `~/.tetora/agents/{name}/SOUL.md` ou exécuter `tetora init` |
| `Workspace: not found` | Exécuter `tetora init` pour créer la structure de répertoires |

---

## "session produced no output"

Une tâche se termine mais la sortie est vide. La tâche est automatiquement marquée `failed`.

**Cause 1 : Fenêtre de contexte trop grande.** Le prompt injecté dans la session a dépassé la limite de contexte du modèle. Claude Code se ferme immédiatement quand il ne peut pas faire tenir le contexte.

Correction : activez la compression de session dans `config.json` :

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

Ou réduisez la quantité de contexte injectée dans la tâche (description plus courte, moins de commentaires spec, chaîne `dependsOn` plus petite).

**Cause 2 : Échec au démarrage du CLI Claude Code.** Le binaire à `claudePath` plante au démarrage — généralement dû à une mauvaise clé API, un problème réseau ou une incompatibilité de version.

Correction : exécutez le binaire Claude Code manuellement pour voir l'erreur :

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Corrigez l'erreur signalée, puis réessayez la tâche :

```bash
tetora task move task-abc123 --status=todo
```

**Cause 3 : Prompt vide.** La tâche a un titre mais pas de description, et le titre seul est trop ambigu pour que l'agent agisse. La session s'exécute, produit une sortie qui ne satisfait pas la vérification du vide, et est signalée.

Correction : ajoutez une description concrète :

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Erreurs "unauthorized" sur le dashboard

Le dashboard retourne 401 ou affiche une page vide après rechargement.

**Cause 1 : Le Service Worker a mis en cache un ancien token d'authentification.** Le Service Worker PWA met en cache les réponses y compris les en-têtes d'authentification. Après un redémarrage du daemon avec un nouveau token, la version en cache est obsolète.

Correction : rechargez la page en forçant. Dans Chrome/Safari :

- Mac : `Cmd + Shift + R`
- Windows/Linux : `Ctrl + Shift + R`

Ou ouvrez les DevTools → Application → Service Workers → cliquez sur "Unregister", puis rechargez.

**Cause 2 : Non-correspondance de l'en-tête Referer.** Le middleware d'authentification du dashboard valide l'en-tête `Referer`. Les requêtes provenant d'extensions navigateur, de proxies ou de curl sans en-tête `Referer` sont rejetées.

Correction : accédez au dashboard directement à `http://localhost:8991/dashboard`, pas via un proxy. Si vous avez besoin d'un accès API depuis des outils externes, utilisez un token API plutôt que l'authentification de session navigateur.

---

## Le dashboard ne se met pas à jour

Le dashboard se charge mais le flux d'activité, la liste des workers ou le tableau de tâches reste obsolète.

**Cause : Non-correspondance de version du Service Worker.** Le Service Worker PWA sert une version mise en cache du JS/HTML du dashboard même après une mise à jour `make bump`.

Correction :

1. Rechargez en forçant (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. Si cela ne fonctionne pas, ouvrez les DevTools → Application → Service Workers → cliquez sur "Update" ou "Unregister"
3. Rechargez la page

**Cause : Connexion SSE interrompue.** Le dashboard reçoit les mises à jour en direct via Server-Sent Events. Si la connexion se coupe (problème réseau, veille de l'ordinateur portable), le flux cesse de se mettre à jour.

Correction : rechargez la page. La connexion SSE se rétablit automatiquement au chargement de la page.

---

## Avertissement "排程接近滿載"

Vous voyez ce message dans Discord/Telegram ou le flux de notifications du dashboard.

Il s'agit de l'avertissement de slot pressure. Il se déclenche quand les slots de concurrence disponibles descendent à ou sous `warnThreshold` (par défaut : 3). Cela signifie que Tetora fonctionne près de sa capacité maximale.

**Que faire :**

- Si c'est attendu (beaucoup de tâches en cours) : aucune action nécessaire. L'avertissement est informatif.
- Si vous n'avez pas beaucoup de tâches en cours : vérifiez les tâches bloquées en statut `doing` :

```bash
tetora task list --status=doing
```

- Pour augmenter la capacité : augmentez `maxConcurrent` dans `config.json` et ajustez `slotPressure.warnThreshold` en conséquence.
- Si les sessions interactives sont retardées : augmentez `slotPressure.reservedSlots` pour réserver plus de slots pour un usage interactif.

---

## Tâches bloquées en "doing"

Une tâche affiche `status=doing` mais aucun agent ne travaille activement dessus.

**Cause 1 : Daemon redémarré en cours de tâche.** La tâche était en cours d'exécution quand le daemon a été arrêté. Au prochain démarrage, Tetora vérifie les tâches `doing` orphelines et les restaure soit à `done` (s'il existe des preuves de coût/durée) soit les réinitialise à `todo`.

C'est automatique — attendez le prochain démarrage du daemon. Si le daemon est déjà en cours d'exécution et que la tâche est toujours bloquée, le heartbeat ou la réinitialisation des tâches bloquées la prendra en charge dans le délai `stuckThreshold` (par défaut : 2h).

Pour forcer une réinitialisation immédiate :

```bash
tetora task move task-abc123 --status=todo
```

**Cause 2 : Détection de blocage par heartbeat.** Le moniteur heartbeat (`heartbeat.go`) vérifie les sessions en cours. Si une session ne produit aucune sortie pendant le seuil de blocage, elle est automatiquement annulée et la tâche est déplacée vers `failed`.

Vérifiez les commentaires de tâche pour les commentaires système `[auto-reset]` ou `[stall-detected]` :

```bash
tetora task show task-abc123 --full
```

**Annulation manuelle via API :**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Échecs de fusion de worktree

Une tâche se termine et passe à `partial-done` avec un commentaire comme `[worktree] merge failed`.

Cela signifie que les changements de l'agent sur la branche de tâche sont en conflit avec `main`.

**Étapes de récupération :**

```bash
# Voir les détails de la tâche et quelle branche a été créée
tetora task show task-abc123 --full

# Naviguer vers le dépôt du projet
cd /path/to/your/repo

# Fusionner la branche manuellement
git merge feat/kokuyou-task-abc123

# Résoudre les conflits dans votre éditeur, puis committer
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Marquer la tâche comme terminée
tetora task move task-abc123 --status=done
```

Le répertoire worktree est conservé à `~/.tetora/runtime/worktrees/task-abc123/` jusqu'à ce que vous le nettoyiez manuellement ou passiez la tâche à `done`.

---

## Coûts de tokens élevés

Les sessions utilisent plus de tokens que prévu.

**Cause 1 : Contexte non compressé.** Sans compression de session, chaque tour accumule l'historique complet de la conversation. Les tâches longues (nombreux appels d'outils) font croître le contexte de façon linéaire.

Correction : activez `sessionCompaction` (voir la section "session produced no output" ci-dessus).

**Cause 2 : Fichiers de base de connaissances ou de règles volumineux.** Les fichiers dans `workspace/rules/` et `workspace/knowledge/` sont injectés dans chaque prompt d'agent. Si ces fichiers sont volumineux, ils consomment des tokens à chaque appel.

Correction :
- Auditez `workspace/knowledge/` — gardez les fichiers individuels sous 50 Ko.
- Déplacez les documents de référence dont vous avez rarement besoin hors des chemins d'auto-injection.
- Exécutez `tetora knowledge list` pour voir ce qui est injecté et sa taille.

**Cause 3 : Routage vers le mauvais modèle.** Un modèle coûteux (Opus) est utilisé pour des tâches de routine.

Correction : révisez `defaultModel` dans la configuration de l'agent et définissez un modèle moins cher par défaut pour les tâches en masse :

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Erreurs de timeout du provider

Les tâches échouent avec des erreurs de timeout comme `context deadline exceeded` ou `provider request timed out`.

**Cause 1 : Timeout de tâche trop court.** Le timeout par défaut peut être trop court pour les tâches complexes.

Correction : définissez un timeout plus long dans la configuration de l'agent de la tâche ou par tâche :

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

Ou augmentez l'estimation de timeout LLM en ajoutant plus de détails à la description de la tâche (Tetora utilise la description pour estimer le timeout via un appel de modèle rapide).

**Cause 2 : Limitation de débit API ou contention.** Trop de requêtes simultanées atteignant le même provider.

Correction : réduisez `maxConcurrentTasks` ou ajoutez un `maxBudget` pour limiter les tâches coûteuses :

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` a interrompu un workflow

Vous avez exécuté `make bump` pendant qu'un workflow ou une tâche était en cours d'exécution. Le daemon a redémarré en cours de tâche.

Le redémarrage déclenche la logique de récupération des orphelins de Tetora :

- Les tâches avec des preuves de complétion (coût enregistré, durée enregistrée) sont restaurées à `done`.
- Les tâches sans preuve de complétion mais au-delà du délai de grâce (2 minutes) sont réinitialisées à `todo` pour un nouveau dispatch.
- Les tâches mises à jour dans les 2 dernières minutes sont laissées telles quelles jusqu'au prochain scan des tâches bloquées.

**Pour vérifier ce qui s'est passé :**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Consultez les commentaires de tâche pour les entrées `[auto-restore]` ou `[auto-reset]`.

**Si vous devez éviter les bumps pendant les tâches actives** (pas encore disponible comme indicateur), vérifiez qu'aucune tâche n'est en cours avant de bumper :

```bash
tetora task list --status=doing
# Si vide, safe to bump
make bump
```

---

## Erreurs SQLite

Vous voyez des erreurs comme `database is locked`, `SQLITE_BUSY`, ou `index.lock` dans les logs.

**Cause 1 : Pragma WAL mode manquant.** Sans mode WAL, SQLite utilise un verrouillage exclusif des fichiers, ce qui provoque `database is locked` lors de lectures/écritures concurrentes.

Tous les appels DB Tetora passent par `queryDB()` et `execDB()` qui ajoutent `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`. Si vous appelez sqlite3 directement dans des scripts, ajoutez ces pragmas :

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Cause 2 : Fichier `index.lock` obsolète.** Les opérations git laissent `index.lock` si elles sont interrompues. Le gestionnaire de worktree vérifie les locks obsolètes avant de démarrer le travail git, mais un crash peut en laisser un.

Correction :

```bash
# Trouver les fichiers lock obsolètes
find ~/.tetora/runtime/worktrees -name "index.lock"

# Les supprimer (uniquement si aucune opération git n'est activement en cours)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram ne répond pas

Les messages au bot ne reçoivent pas de réponse.

**Cause 1 : Mauvaise configuration des canaux.** Discord dispose de deux listes de canaux : `channelIDs` (répond directement à tous les messages) et `mentionChannelIDs` (répond uniquement quand @-mentionné). Si un canal n'est dans aucune des deux listes, les messages sont ignorés.

Correction : vérifiez `config.json` :

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Cause 2 : Token de bot expiré ou incorrect.** Les tokens de bot Telegram n'expirent pas, mais les tokens Discord peuvent être invalidés si le bot est expulsé du serveur ou si le token est régénéré.

Correction : recréez le token du bot dans le portail développeur Discord et mettez à jour `config.json`.

**Cause 3 : Daemon non en cours d'exécution.** La passerelle bot n'est active que quand `tetora serve` est en cours d'exécution.

Correction :

```bash
tetora status
tetora serve   # si non en cours d'exécution
```

---

## Erreurs CLI glab / gh

L'intégration git échoue avec des erreurs provenant de `glab` ou `gh`.

**Erreur courante : `gh: command not found`**

Correction :
```bash
brew install gh      # macOS
gh auth login        # s'authentifier
```

**Erreur courante : `glab: You are not logged in`**

Correction :
```bash
brew install glab    # macOS
glab auth login      # s'authentifier avec votre instance GitLab
```

**Erreur courante : `remote: HTTP Basic: Access denied`**

Correction : vérifiez que votre clé SSH ou identifiant HTTPS est configuré pour l'hôte du dépôt. Pour GitLab :

```bash
glab auth status
ssh -T git@gitlab.com   # tester la connectivité SSH
```

Pour GitHub :

```bash
gh auth status
ssh -T git@github.com
```

**La création de PR/MR réussit mais pointe vers la mauvaise branche de base**

Par défaut, les PR ciblent la branche par défaut du dépôt (`main` ou `master`). Si votre workflow utilise une base différente, définissez-la explicitement dans votre configuration git post-tâche ou assurez-vous que la branche par défaut du dépôt est correctement configurée sur la plateforme d'hébergement.
