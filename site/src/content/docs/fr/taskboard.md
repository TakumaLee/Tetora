---
title: "Guide du Taskboard et de l'auto-dispatch"
lang: "fr"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Guide du Taskboard et de l'auto-dispatch

## Vue d'ensemble

Le Taskboard est le système kanban intégré de Tetora pour le suivi et l'exécution automatique des tâches. Il associe un store de tâches persistant (basé sur SQLite) à un moteur d'auto-dispatch qui surveille les tâches prêtes et les confie aux agents sans intervention manuelle.

Cas d'utilisation typiques :

- Constituer un backlog de tâches d'ingénierie et laisser les agents les traiter pendant la nuit
- Router les tâches vers des agents spécifiques selon leurs compétences (ex. `kokuyou` pour le backend, `kohaku` pour le contenu)
- Enchaîner les tâches avec des relations de dépendance afin que les agents reprennent là où d'autres se sont arrêtés
- Intégrer l'exécution des tâches avec git : création automatique de branches, commit, push et PR/MR

**Prérequis :** `taskBoard.enabled: true` dans `config.json` et le daemon Tetora en cours d'exécution.

---

## Cycle de vie des tâches

Les tâches progressent dans les statuts suivants :

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Statut | Signification |
|---|---|
| `idea` | Concept brut, pas encore affiné |
| `needs-thought` | Nécessite une analyse ou une conception avant l'implémentation |
| `backlog` | Définie et priorisée, mais pas encore planifiée |
| `todo` | Prête à exécuter — l'auto-dispatch la prendra en charge si un responsable est défini |
| `doing` | En cours d'exécution |
| `review` | Exécution terminée, en attente d'une révision qualité |
| `done` | Terminée et révisée |
| `partial-done` | Exécution réussie mais le post-traitement a échoué (ex. conflit de fusion git). Récupérable. |
| `failed` | Exécution échouée ou sortie vide. Sera retentée jusqu'à `maxRetries`. |

L'auto-dispatch prend en charge les tâches avec `status=todo`. Si une tâche n'a pas de responsable, elle est automatiquement assignée à `defaultAgent` (par défaut : `ruri`). Les tâches en `backlog` sont triées périodiquement par le `backlogAgent` configuré (par défaut : `ruri`), qui fait progresser les plus prometteuses vers `todo`.

---

## Création de tâches

### CLI

```bash
# Tâche minimale (atterrit dans le backlog, non assignée)
tetora task create --title="Add rate limiting to API"

# Avec toutes les options
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# Lister les tâches
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Afficher une tâche spécifique
tetora task show task-abc123
tetora task show task-abc123 --full   # inclut les commentaires/thread

# Déplacer une tâche manuellement
tetora task move task-abc123 --status=todo

# Assigner à un agent
tetora task assign task-abc123 --assignee=kokuyou

# Ajouter un commentaire (types : spec, context, log ou system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Les IDs de tâches sont générés automatiquement au format `task-<uuid>`. Vous pouvez référencer une tâche par son ID complet ou un préfixe court — la CLI suggèrera des correspondances.

### API HTTP

```bash
# Créer
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# Lister (filtrer par statut)
curl "http://localhost:8991/api/tasks?status=todo"

# Déplacer vers un nouveau statut
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Ouvrez l'onglet **Taskboard** dans le dashboard (`http://localhost:8991/dashboard`). Les tâches sont affichées en colonnes kanban. Faites glisser les cartes entre les colonnes pour changer le statut, cliquez sur une carte pour ouvrir le panneau de détails avec les commentaires et la vue diff.

---

## Auto-dispatch

L'auto-dispatch est la boucle en arrière-plan qui prend en charge les tâches `todo` et les exécute via les agents.

### Fonctionnement

1. Un ticker se déclenche toutes les `interval` (par défaut : `5m`).
2. Le scanner vérifie combien de tâches sont en cours. Si `activeCount >= maxConcurrentTasks`, le scan est ignoré.
3. Pour chaque tâche `todo` avec un responsable, la tâche est dispatchée vers cet agent. Les tâches sans responsable sont auto-assignées à `defaultAgent`.
4. Quand une tâche se termine, un nouveau scan immédiat est lancé pour que le prochain lot démarre sans attendre l'intervalle complet.
5. Au démarrage du daemon, les tâches `doing` orphelines d'un crash précédent sont soit restaurées à `done` (s'il existe des preuves de complétion) soit réinitialisées à `todo` (si vraiment orphelines).

### Flux de dispatch

```
                          ┌─────────┐
                          │  idea   │  (saisie de concept manuel)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (nécessite une analyse)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, par défaut : ruri) s'exécute       │
  │  périodiquement :                                         │
  │   • "ready"     → assigner agent → déplacer vers todo     │
  │   • "decompose" → créer sous-tâches → parent vers doing   │
  │   • "clarify"   → ajouter question en commentaire         │
  │                    → rester dans backlog                  │
  │                                                           │
  │  Chemin rapide : a déjà un responsable + pas de deps      │
  │  bloquantes → passer le triage LLM, promouvoir vers todo  │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  L'auto-dispatch prend les tâches à chaque cycle :        │
  │   • A un responsable     → dispatcher vers cet agent      │
  │   • Pas de responsable   → assigner defaultAgent, lancer  │
  │   • A un workflow        → exécuter via pipeline workflow  │
  │   • A des dependsOn      → attendre que les deps soient   │
  │                             done                          │
  │   • Exécution précédente → reprendre depuis checkpoint    │
  │     reprenableen                                          │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  L'agent exécute la tâche (prompt unique ou DAG workflow) │
  │                                                           │
  │  Garde : stuckThreshold (par défaut 2h)                   │
  │   • Si workflow toujours en cours → rafraîchir horodatage │
  │   • Si vraiment bloqué            → réinitialiser à todo  │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     succès    échec partiel  échec
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │   Bouton reprendre    │  Nouvelle tentative (jusqu'à maxRetries)
           │   dans le dashboard   │  ou escalade
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalade │
       └────────┘            │  humaine │
                             └──────────┘
```

### Détails du triage

Le triage s'exécute toutes les `backlogTriageInterval` (par défaut : `1h`) et est effectué par le `backlogAgent` (par défaut : `ruri`). L'agent reçoit chaque tâche du backlog avec ses commentaires et la liste des agents disponibles, puis décide :

| Action | Effet |
|---|---|
| `ready` | Assigne un agent spécifique et fait progresser vers `todo` |
| `decompose` | Crée des sous-tâches (avec responsables), le parent passe à `doing` |
| `clarify` | Ajoute une question en commentaire, la tâche reste dans `backlog` |

**Chemin rapide** : les tâches qui ont déjà un responsable et pas de dépendances bloquantes ignorent entièrement le triage LLM et sont promues directement vers `todo`.

### Auto-assignation

Quand une tâche `todo` n'a pas de responsable, le dispatcher l'assigne automatiquement à `defaultAgent` (configurable, par défaut : `ruri`). Cela évite que des tâches restent silencieusement bloquées. Le flux typique :

1. Tâche créée sans responsable → entre dans `backlog`
2. Le triage la fait progresser vers `todo` (avec ou sans assignation d'un agent)
3. Si le triage n'a pas assigné → le dispatcher assigne `defaultAgent`
4. La tâche s'exécute normalement

### Configuration

À ajouter dans `config.json` :

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| Champ | Défaut | Description |
|---|---|---|
| `enabled` | `false` | Active la boucle d'auto-dispatch |
| `interval` | `5m` | Fréquence de recherche des tâches prêtes |
| `maxConcurrentTasks` | `3` | Nombre maximum de tâches exécutées simultanément |
| `defaultAgent` | `ruri` | Auto-assigné aux tâches `todo` sans responsable avant le dispatch |
| `backlogAgent` | `ruri` | Agent qui révise et fait progresser les tâches du backlog |
| `reviewAgent` | `ruri` | Agent qui révise la sortie des tâches terminées |
| `escalateAssignee` | `takuma` | Qui reçoit l'assignation quand la révision automatique demande un jugement humain |
| `stuckThreshold` | `2h` | Durée maximale qu'une tâche peut rester en `doing` avant réinitialisation |
| `backlogTriageInterval` | `1h` | Intervalle minimum entre les exécutions de triage du backlog |
| `reviewLoop` | `false` | Active la boucle Dev↔QA (exécuter → réviser → corriger, jusqu'à `maxRetries`) |
| `maxBudget` | sans limite | Coût maximum par tâche en USD |
| `defaultModel` | — | Surcharge le modèle pour toutes les tâches auto-dispatchées |

---

## Slot Pressure

La slot pressure empêche l'auto-dispatch de consommer tous les slots de concurrence et de priver les sessions interactives (messages de chat humains, dispatches à la demande).

Activez-la dans `config.json` :

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| Champ | Défaut | Description |
|---|---|---|
| `reservedSlots` | `2` | Slots réservés pour un usage interactif. Les tâches non interactives doivent attendre si les slots disponibles descendent à ce niveau. |
| `warnThreshold` | `3` | Un avertissement se déclenche quand les slots disponibles descendent à ce niveau. Le message "排程接近滿載" apparaît dans le dashboard et les canaux de notification. |
| `nonInteractiveTimeout` | `5m` | Durée pendant laquelle une tâche non interactive attend un slot avant d'être annulée. |

Les sources interactives (chat humain, `tetora dispatch`, `tetora route`) acquièrent toujours des slots immédiatement. Les sources en arrière-plan (taskboard, cron) attendent si la pression est élevée.

---

## Intégration git

Quand `gitCommit`, `gitPush` et `gitPR` sont activés, le dispatcher exécute des opérations git après la réussite d'une tâche.

**Le nommage des branches** est contrôlé par `gitWorkflow.branchConvention` :

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

Le template par défaut `{type}/{agent}-{description}` produit des branches comme `feat/kokuyou-add-rate-limiting`. La partie `{description}` est dérivée du titre de la tâche (en minuscules, espaces remplacés par des tirets, tronquée à 40 caractères).

Le champ `type` d'une tâche définit le préfixe de branche. Si une tâche n'a pas de type, `defaultType` est utilisé.

**La création automatique de PR/MR** supporte GitHub (`gh`) et GitLab (`glab`). Le binaire disponible dans `PATH` est utilisé automatiquement.

---

## Mode worktree

Quand `gitWorktree: true`, chaque tâche s'exécute dans un worktree git isolé plutôt que dans le répertoire de travail partagé. Cela élimine les conflits de fichiers quand plusieurs tâches s'exécutent simultanément sur le même dépôt.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← copie isolée pour cette tâche
  task-def456/   ← copie isolée pour cette tâche
```

À la fin d'une tâche :

- Si `autoMerge: true` (par défaut), la branche du worktree est fusionnée dans `main` et le worktree est supprimé.
- Si la fusion échoue, la tâche passe au statut `partial-done`. Le worktree est conservé pour une résolution manuelle.

Pour récupérer depuis `partial-done` :

```bash
# Inspecter ce qui s'est passé
tetora task show task-abc123 --full

# Fusionner la branche manuellement
git merge feat/kokuyou-add-rate-limiting

# Résoudre les conflits dans votre éditeur, puis committer
git add .
git commit -m "merge: feat/kokuyou-add-rate-limiting"

# Marquer la tâche comme terminée
tetora task move task-abc123 --status=done
```

---

## Intégration des workflows

Les tâches peuvent s'exécuter via un pipeline de workflow plutôt qu'un seul prompt d'agent. C'est utile quand une tâche nécessite plusieurs étapes coordonnées (ex. recherche → implémentation → test → documentation).

Assigner un workflow à une tâche :

```bash
# Définir à la création de la tâche
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# Ou mettre à jour une tâche existante
tetora task update task-abc123 --workflow=engineering-pipeline
```

Pour désactiver le workflow par défaut du tableau pour une tâche spécifique :

```json
{ "workflow": "none" }
```

Un workflow par défaut au niveau du tableau s'applique à toutes les tâches auto-dispatchées sauf surcharge :

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

Le champ `workflowRunId` sur la tâche la relie à l'exécution spécifique du workflow, visible dans l'onglet Workflows du dashboard.

---

## Vues du dashboard

Ouvrez le dashboard à `http://localhost:8991/dashboard` et naviguez vers l'onglet **Taskboard**.

**Tableau kanban** — colonnes pour chaque statut. Les cartes affichent le titre, le responsable, le badge de priorité et le coût. Faites glisser pour changer le statut.

**Panneau de détail de tâche** — cliquez sur une carte pour l'ouvrir. Affiche :
- Description complète et tous les commentaires (spec, context, entrées de log)
- Lien de session (accède au terminal du worker en direct si encore en cours)
- Coût, durée, nombre de tentatives
- Lien vers l'exécution du workflow si applicable

**Panneau de révision diff** — quand `requireReview: true`, les tâches terminées apparaissent dans une file de révision. Les réviseurs voient le diff des changements et peuvent approuver ou demander des modifications.

---

## Conseils

**Dimensionnement des tâches.** Limitez les tâches à une portée de 30 à 90 minutes. Les tâches trop importantes (refactorisations sur plusieurs jours) ont tendance à expirer ou produire une sortie vide, ce qui les fait marquer comme échouées. Décomposez-les en sous-tâches en utilisant le champ `parentId`.

**Limites de dispatch simultané.** `maxConcurrentTasks: 3` est une valeur par défaut sûre. L'augmenter au-delà du nombre de connexions API que votre provider autorise provoque de la contention et des timeouts. Commencez à 3, passez à 5 uniquement après avoir confirmé un comportement stable.

**Récupération depuis partial-done.** Si une tâche entre en `partial-done`, l'agent a terminé son travail avec succès — seule l'étape de fusion git a échoué. Résolvez le conflit manuellement, puis passez la tâche à `done`. Le coût et les données de session sont préservés.

**Utilisation de `dependsOn`.** Les tâches avec des dépendances non satisfaites sont ignorées par le dispatcher jusqu'à ce que tous les IDs de tâches listés atteignent le statut `done`. Les résultats des tâches en amont sont automatiquement injectés dans le prompt de la tâche dépendante sous "Previous Task Results".

**Triage du backlog.** Le `backlogAgent` lit chaque tâche `backlog`, évalue la faisabilité et la priorité, et fait progresser les tâches claires vers `todo`. Rédigez des descriptions détaillées et des critères d'acceptation dans vos tâches `backlog` — l'agent de triage s'en sert pour décider de les promouvoir ou de les laisser pour une révision humaine.

**Tentatives et boucle de révision.** Avec `reviewLoop: false` (par défaut), une tâche échouée est retentée jusqu'à `maxRetries` fois avec les commentaires de log précédents injectés. Avec `reviewLoop: true`, chaque exécution est révisée par le `reviewAgent` avant d'être considérée terminée — l'agent reçoit des retours et réessaie si des problèmes sont trouvés.
