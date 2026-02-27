# Workflows

## Vue d'ensemble

Les workflows sont le système d'orchestration de tâches multi-étapes de Tetora. Définissez une séquence d'étapes en JSON, faites collaborer différents agents et automatisez des tâches complexes.

**Cas d'usage :**

- Tâches nécessitant plusieurs agents travaillant de manière séquentielle ou en parallèle
- Processus avec branchement conditionnel et logique de nouvelle tentative en cas d'erreur
- Travail automatisé déclenché par des cron, des événements ou des webhooks
- Processus formels nécessitant un historique d'exécution et un suivi des coûts

## Démarrage Rapide

### 1. Écrire le JSON du workflow

Créez `my-workflow.json` :

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. Importer et valider

```bash
# Valider la structure JSON
tetora workflow validate my-workflow.json

# Importer dans ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Exécuter

```bash
# Exécuter le workflow
tetora workflow run research-and-summarize

# Substituer des variables
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (aucun appel LLM, estimation des coûts uniquement)
tetora workflow run research-and-summarize --dry-run
```

### 4. Consulter les résultats

```bash
# Lister l'historique des exécutions
tetora workflow runs research-and-summarize

# Afficher le statut détaillé d'une exécution spécifique
tetora workflow status <run-id>
```

## Structure du JSON du Workflow

### Champs de Niveau Supérieur

| Champ | Type | Requis | Description |
|-------|------|:------:|-------------|
| `name` | string | Oui | Nom du workflow. Alphanumérique, `-` et `_` uniquement (ex. `my-workflow`) |
| `description` | string | | Description |
| `steps` | WorkflowStep[] | Oui | Au moins une étape |
| `variables` | map[string]string | | Variables d'entrée avec valeurs par défaut (`""` vide = requis) |
| `timeout` | string | | Délai global au format de durée Go (ex. `"30m"`, `"1h"`) |
| `onSuccess` | string | | Modèle de notification en cas de succès |
| `onFailure` | string | | Modèle de notification en cas d'échec |

### Champs de WorkflowStep

| Champ | Type | Description |
|-------|------|-------------|
| `id` | string | **Requis** — Identifiant unique de l'étape |
| `type` | string | Type d'étape, par défaut `"dispatch"`. Voir les types ci-dessous |
| `agent` | string | Rôle de l'agent exécutant cette étape |
| `prompt` | string | Instruction pour l'agent (supporte les modèles `{{}}`) |
| `skill` | string | Nom du skill (pour type=skill) |
| `skillArgs` | string[] | Arguments du skill (supporte les modèles) |
| `dependsOn` | string[] | IDs des étapes prérequises (dépendances DAG) |
| `model` | string | Substitution du modèle LLM |
| `provider` | string | Substitution du fournisseur |
| `timeout` | string | Délai par étape |
| `budget` | number | Limite de coût (USD) |
| `permissionMode` | string | Mode de permissions |
| `if` | string | Expression de condition (type=condition) |
| `then` | string | ID de l'étape vers laquelle sauter si la condition est vraie |
| `else` | string | ID de l'étape vers laquelle sauter si la condition est fausse |
| `handoffFrom` | string | ID de l'étape source (type=handoff) |
| `parallel` | WorkflowStep[] | Sous-étapes à exécuter en parallèle (type=parallel) |
| `retryMax` | int | Nombre maximum de nouvelles tentatives (nécessite `onError: "retry"`) |
| `retryDelay` | string | Intervalle entre les tentatives, ex. `"10s"` |
| `onError` | string | Gestion des erreurs : `"stop"` (par défaut), `"skip"`, `"retry"` |
| `toolName` | string | Nom de l'outil (type=tool_call) |
| `toolInput` | map[string]string | Paramètres d'entrée de l'outil (supporte l'expansion `{{var}}`) |
| `delay` | string | Durée d'attente (type=delay), ex. `"30s"`, `"5m"` |
| `notifyMsg` | string | Message de notification (type=notify, supporte les modèles) |
| `notifyTo` | string | Indication du canal de notification (ex. `"telegram"`) |

## Types d'Étapes

### dispatch (par défaut)

Envoie un prompt à l'agent spécifié pour exécution. C'est le type d'étape le plus courant, utilisé lorsque `type` est omis.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Requis :** `prompt`
**Optionnel :** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Exécute un skill enregistré.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Requis :** `skill`
**Optionnel :** `skillArgs`

### condition

Évalue une expression de condition pour déterminer la branche. Si vraie, emprunte `then` ; si fausse, emprunte `else`. La branche non choisie est marquée comme ignorée.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Requis :** `if`, `then`
**Optionnel :** `else`

Opérateurs supportés :
- `==` — égal (ex. `{{type}} == 'technical'`)
- `!=` — différent
- Vérification de véracité — non vide et différent de `"false"`/`"0"` est évalué à vrai

### parallel

Exécute plusieurs sous-étapes de manière concurrente, en attendant que toutes se terminent. Les sorties des sous-étapes sont jointes avec `\n---\n`.

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**Requis :** `parallel` (au moins une sous-étape)

Les résultats individuels des sous-étapes peuvent être référencés via `{{steps.search-papers.output}}`.

### handoff

Transmet la sortie d'une étape à un autre agent pour un traitement ultérieur. La sortie complète de l'étape source devient le contexte de l'agent destinataire.

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**Requis :** `handoffFrom`, `agent`
**Optionnel :** `prompt` (instruction pour l'agent destinataire)

### tool_call

Invoque un outil enregistré dans le registre d'outils.

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**Requis :** `toolName`
**Optionnel :** `toolInput` (supporte l'expansion `{{var}}`)

### delay

Attend une durée définie avant de continuer.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Requis :** `delay` (format de durée Go : `"30s"`, `"5m"`, `"1h"`)

### notify

Envoie un message de notification. Le message est publié sous forme d'événement SSE (type=`workflow_notify`) afin que des consommateurs externes puissent déclencher des actions sur Telegram, Slack, etc.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Requis :** `notifyMsg`
**Optionnel :** `notifyTo` (indication du canal)

## Variables et Modèles

Les workflows supportent la syntaxe de modèle `{{}}`, développée avant l'exécution de chaque étape.

### Variables d'Entrée

```
{{varName}}
```

Résolues à partir des valeurs par défaut de `variables` ou des substitutions `--var key=value`.

### Résultats des Étapes

```
{{steps.ID.output}}    — Texte de sortie de l'étape
{{steps.ID.status}}    — Statut de l'étape (success/error/skipped/timeout)
{{steps.ID.error}}     — Message d'erreur de l'étape
```

### Variables d'Environnement

```
{{env.KEY}}            — Variable d'environnement système
```

### Exemple

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Dépendances et Contrôle du Flux

### dependsOn — Dépendances DAG

Utilisez `dependsOn` pour définir l'ordre d'exécution. Le système trie automatiquement les étapes sous forme de DAG (Graphe Orienté Acyclique).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` attend que `step-a` et `step-b` soient tous deux terminés
- Les étapes sans `dependsOn` démarrent immédiatement (éventuellement en parallèle)
- Les dépendances circulaires sont détectées et rejetées

### Branchement Conditionnel

Les champs `then`/`else` d'une étape `condition` déterminent quelles étapes en aval sont exécutées :

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

L'étape de la branche non choisie est marquée comme `skipped`. Les étapes en aval sont toujours évaluées normalement selon leur `dependsOn`.

## Gestion des Erreurs

### Stratégies onError

Chaque étape peut définir `onError` :

| Valeur | Comportement |
|--------|--------------|
| `"stop"` | **Par défaut** — Interrompt le workflow en cas d'échec ; les étapes restantes sont marquées comme ignorées |
| `"skip"` | Marque l'étape échouée comme ignorée et continue |
| `"retry"` | Effectue de nouvelles tentatives selon `retryMax` + `retryDelay` ; si toutes échouent, traité comme une erreur |

### Configuration des Nouvelles Tentatives

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax` : Nombre maximum de tentatives supplémentaires (sans compter la première tentative)
- `retryDelay` : Délai entre les tentatives, par défaut 5 secondes
- Uniquement effectif lorsque `onError: "retry"`

## Triggers

Les triggers permettent l'exécution automatique des workflows. Configurez-les dans `config.json` sous le tableau `workflowTriggers`.

### Structure de WorkflowTriggerConfig

| Champ | Type | Description |
|-------|------|-------------|
| `name` | string | Nom du trigger |
| `workflowName` | string | Workflow à exécuter |
| `enabled` | bool | Si activé (par défaut : true) |
| `trigger` | TriggerSpec | Condition du trigger |
| `variables` | map[string]string | Substitutions de variables pour le workflow |
| `cooldown` | string | Période de refroidissement (ex. `"5m"`, `"1h"`) |

### Structure de TriggerSpec

| Champ | Type | Description |
|-------|------|-------------|
| `type` | string | `"cron"`, `"event"` ou `"webhook"` |
| `cron` | string | Expression cron (5 champs : min heure jour mois joursemaine) |
| `tz` | string | Fuseau horaire (ex. `"Asia/Taipei"`), pour cron uniquement |
| `event` | string | Type d'événement SSE, supporte le joker avec suffixe `*` (ex. `"deploy_*"`) |
| `webhook` | string | Suffixe du chemin webhook |

### Triggers Cron

Vérifiés toutes les 30 secondes, se déclenchent au plus une fois par minute (avec déduplication).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Triggers d'Événement

Écoute sur le canal SSE `_triggers` et compare les types d'événements. Supporte le joker avec suffixe `*`.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Les triggers d'événement injectent automatiquement des variables supplémentaires : `event_type`, `task_id`, `session_id`, ainsi que les champs de données de l'événement (préfixés par `event_`).

### Triggers Webhook

Déclenchés via HTTP POST :

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Utilisation :

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

Les paires clé-valeur JSON du corps du POST sont injectées comme variables supplémentaires du workflow.

### Cooldown

Tous les triggers supportent `cooldown` pour éviter des déclenchements répétés sur une courte période. Les triggers survenant pendant le cooldown sont silencieusement ignorés.

### Méta-Variables des Triggers

Le système injecte automatiquement ces variables à chaque déclenchement :

- `_trigger_name` — Nom du trigger
- `_trigger_type` — Type de trigger (cron/event/webhook)
- `_trigger_time` — Heure du déclenchement (RFC3339)

## Modes d'Exécution

### live (par défaut)

Exécution complète : appelle les LLMs, enregistre l'historique, publie des événements SSE.

```bash
tetora workflow run my-workflow
```

### dry-run

Aucun appel LLM ; estime le coût de chaque étape. Les étapes de condition sont évaluées normalement ; les étapes dispatch/skill/handoff retournent des estimations de coût.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Exécute les appels LLM normalement mais n'enregistre pas dans l'historique des tâches ni dans les journaux de session. Utile pour les tests.

```bash
tetora workflow run my-workflow --shadow
```

## Référence CLI

```
tetora workflow <command> [options]
```

| Commande | Description |
|----------|-------------|
| `list` | Lister tous les workflows enregistrés |
| `show <name>` | Afficher la définition d'un workflow (résumé + JSON) |
| `validate <name\|file>` | Valider un workflow (accepte un nom ou un chemin de fichier JSON) |
| `create <file>` | Importer un workflow depuis un fichier JSON (valide d'abord) |
| `delete <name>` | Supprimer un workflow |
| `run <name> [flags]` | Exécuter un workflow |
| `runs [name]` | Lister l'historique des exécutions (filtrer optionnellement par nom) |
| `status <run-id>` | Afficher le statut détaillé d'une exécution (sortie JSON) |
| `messages <run-id>` | Afficher les messages d'agent et les enregistrements de handoff d'une exécution |
| `history <name>` | Afficher l'historique des versions du workflow |
| `rollback <name> <version-id>` | Restaurer vers une version spécifique |
| `diff <version1> <version2>` | Comparer deux versions |

### Options de la Commande run

| Option | Description |
|--------|-------------|
| `--var key=value` | Substituer une variable du workflow (peut être utilisé plusieurs fois) |
| `--dry-run` | Mode dry-run (aucun appel LLM) |
| `--shadow` | Mode shadow (pas d'enregistrement dans l'historique) |

### Alias

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## Référence de l'API HTTP

### CRUD des Workflows

| Méthode | Chemin | Description |
|---------|--------|-------------|
| GET | `/workflows` | Lister tous les workflows |
| POST | `/workflows` | Créer un workflow (corps : JSON du Workflow) |
| GET | `/workflows/{name}` | Obtenir la définition d'un workflow |
| DELETE | `/workflows/{name}` | Supprimer un workflow |
| POST | `/workflows/{name}/validate` | Valider un workflow |
| POST | `/workflows/{name}/run` | Exécuter un workflow (asynchrone, retourne `202 Accepted`) |
| GET | `/workflows/{name}/runs` | Obtenir l'historique des exécutions d'un workflow |

#### Corps de POST /workflows/{name}/run

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Exécutions de Workflows

| Méthode | Chemin | Description |
|---------|--------|-------------|
| GET | `/workflow-runs` | Lister tous les enregistrements d'exécution (ajouter `?workflow=name` pour filtrer) |
| GET | `/workflow-runs/{id}` | Obtenir les détails d'une exécution (inclut les handoffs + messages d'agent) |

### Triggers

| Méthode | Chemin | Description |
|---------|--------|-------------|
| GET | `/api/triggers` | Lister le statut de tous les triggers |
| POST | `/api/triggers/{name}/fire` | Déclencher manuellement un trigger |
| GET | `/api/triggers/{name}/runs` | Voir l'historique des exécutions d'un trigger (ajouter `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Trigger webhook (corps : variables JSON clé-valeur) |

## Gestion des Versions

Chaque opération `create` ou modification crée automatiquement un snapshot de version.

```bash
# Voir l'historique des versions
tetora workflow history my-workflow

# Restaurer vers une version spécifique
tetora workflow rollback my-workflow <version-id>

# Comparer deux versions
tetora workflow diff <version-id-1> <version-id-2>
```

## Règles de Validation

Le système valide avant `create` et avant `run` :

- `name` est requis ; seuls les caractères alphanumériques, `-` et `_` sont autorisés
- Au moins une étape est requise
- Les IDs d'étape doivent être uniques
- Les références dans `dependsOn` doivent pointer vers des IDs d'étape existants
- Les étapes ne peuvent pas dépendre d'elles-mêmes
- Les dépendances circulaires sont rejetées (détection de cycles dans le DAG)
- Champs requis par type d'étape (ex. dispatch nécessite `prompt`, condition nécessite `if` + `then`)
- `timeout`, `retryDelay` et `delay` doivent être dans un format de durée Go valide
- `onError` accepte uniquement `stop`, `skip`, `retry`
- `then`/`else` dans condition doivent référencer des IDs d'étape existants
- `handoffFrom` dans handoff doit référencer un ID d'étape existant
