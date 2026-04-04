<p align="center">
  <img src="assets/banner.png" alt="Tetora — Orchestrateur d'Agents IA" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | **Français** | [Deutsch](README.de.md)

<p align="center">
  <strong>Plateforme d'assistant IA auto-hébergée avec architecture multi-agents.</strong>
</p>

Tetora s'exécute en tant que binaire Go unique sans aucune dépendance externe. Il se connecte aux fournisseurs d'IA que vous utilisez déjà, s'intègre aux plateformes de messagerie utilisées par votre équipe et conserve toutes les données sur votre propre matériel.

---

## Qu'est-ce que Tetora

Tetora est un orchestrateur d'agents IA qui vous permet de définir plusieurs rôles d'agents -- chacun avec sa propre personnalité, prompt système, modèle et accès aux outils -- et d'interagir avec eux via des plateformes de chat, des APIs HTTP ou la ligne de commande.

**Capacités principales :**

- **Rôles multi-agents** -- définissez des agents distincts avec des personnalités, budgets et permissions d'outils séparés
- **Multi-fournisseur** -- Claude API, OpenAI, Gemini et plus ; échangez ou combinez librement
- **Multi-plateforme** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron jobs** -- planifiez des tâches récurrentes avec des portes d'approbation et des notifications
- **Base de connaissances** -- fournissez des documents aux agents pour des réponses fondées
- **Mémoire persistante** -- les agents se souviennent du contexte entre les sessions ; couche de mémoire unifiée avec consolidation
- **Support MCP** -- connectez des serveurs Model Context Protocol en tant que fournisseurs d'outils
- **Skills et workflows** -- paquets de compétences composables et pipelines de workflows multi-étapes
- **Tableau de bord web** -- centre de commande CEO avec métriques ROI, bureau en pixels et flux d'activité en direct
- **Moteur de workflows** -- exécution de pipeline basée sur DAG avec branches conditionnelles, étapes parallèles, logique de réessai et routage dynamique de modèles (Sonnet pour les tâches routinières, Opus pour les complexes)
- **Marketplace de modèles** -- onglet Store pour parcourir, importer et exporter des modèles de workflow
- **Dispatch automatique du tableau de tâches** -- tableau Kanban avec attribution automatique des tâches, slots concurrents configurables et système de pression des slots qui réserve de la capacité pour les sessions interactives
- **GitLab MR + GitHub PR** -- création automatique de PR/MR après complétion du workflow ; détection automatique de l'hôte distant
- **Compaction de sessions** -- compression automatique du contexte basée sur les tokens et le nombre de messages pour maintenir les sessions dans les limites du modèle
- **Service Worker PWA** -- tableau de bord hors ligne avec mise en cache intelligente
- **Statut partiellement terminé** -- les tâches qui se terminent mais échouent au post-traitement (git merge, revue) entrent dans un état intermédiaire récupérable au lieu d'être perdues
- **Webhooks** -- déclenchez des actions d'agents depuis des systèmes externes
- **Gouvernance des coûts** -- budgets par rôle et globaux avec rétrogradation automatique de modèle
- **Rétention des données** -- politiques de nettoyage configurables par table, avec export et purge complets
- **Plugins** -- étendez les fonctionnalités via des processus de plugins externes
- **Rappels intelligents, habitudes, objectifs, contacts, suivi financier, briefings et plus encore**

---

## Démarrage Rapide

### Pour les ingénieurs

```bash
# Installer la dernière version
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Lancer l'assistant de configuration
tetora init

# Vérifier que tout est correctement configuré
tetora doctor

# Démarrer le daemon
tetora serve
```

### Pour les non-ingénieurs

1. Rendez-vous sur la [page des Releases](https://github.com/TakumaLee/Tetora/releases/latest)
2. Téléchargez le binaire pour votre plateforme (ex. `tetora-darwin-arm64` pour Mac Apple Silicon)
3. Déplacez-le dans un répertoire de votre PATH et renommez-le en `tetora`, ou placez-le dans `~/.tetora/bin/`
4. Ouvrez un terminal et exécutez :
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agents

Chaque agent Tetora est plus qu'un chatbot -- il possède une identité. Chaque agent (appelé **rôle**) est défini par un **fichier d'âme (soul file)** : un document Markdown qui confère à l'agent sa personnalité, son expertise, son style de communication et ses directives comportementales.

### Définir un rôle

Les rôles sont déclarés dans `config.json` sous la clé `roles` :

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

### Fichiers d'âme (Soul files)

Un fichier d'âme indique à l'agent *qui il est*. Placez-le dans le répertoire de workspace (`~/.tetora/workspace/` par défaut) :

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

### Premiers pas

`tetora init` vous guide dans la création de votre premier rôle et génère automatiquement un fichier d'âme de démarrage. Vous pouvez le modifier à tout moment -- les changements prennent effet à la session suivante.

---

## Tableau de Bord

Tetora intègre un tableau de bord web accessible à `http://localhost:8991/dashboard`. Il est organisé en quatre zones :

| Zone | Contenu |
|------|----------|
| **Centre de Commande** | Résumé exécutif (cartes ROI), sprites d'équipe en pixels, bureau Agent World extensible |
| **Opérations** | Barre d'ops compacte, scorecard des agents + flux d'activité en direct (côte à côte), tâches en cours |
| **Analyses** | Graphique de tendances sur 7 jours, graphiques historiques de débit et de coûts des tâches |
| **Détails d'Ingénierie** | Tableau de bord des coûts, cron jobs, sessions, santé des fournisseurs, confiance, SLA, historique des versions, routage, mémoire et plus (repliable) |

L'éditeur d'agents inclut un **sélecteur de modèles avec reconnaissance du fournisseur** permettant de basculer en un clic entre les modèles cloud et locaux (Ollama). Un **interrupteur global de mode d'inférence** permet de basculer tous les agents entre cloud et local d'un seul bouton. Chaque carte d'agent affiche un badge Cloud/Local et un menu déroulant de changement rapide.

Plusieurs thèmes sont disponibles (Glass, Clean, Material, Boardroom, Retro). Le bureau en pixels Agent World peut être personnalisé avec des décorations et des contrôles de zoom.

```bash
# Ouvrir le tableau de bord dans votre navigateur par défaut
tetora dashboard
```

---

## Commandes Discord

Tetora répond aux commandes préfixées par `!` dans Discord :

| Commande | Description |
|---------|-------------|
| `!model` | Afficher tous les agents groupés par Cloud / Local |
| `!model pick [agent]` | Sélecteur de modèle interactif (boutons + menus déroulants) |
| `!model <model> [agent]` | Définir le modèle directement (détection automatique du fournisseur) |
| `!local [agent]` | Basculer vers les modèles locaux (Ollama) |
| `!cloud [agent]` | Restaurer les modèles cloud |
| `!mode` | Résumé du mode d'inférence avec boutons bascule |
| `!chat <agent>` | Verrouiller le canal sur un agent spécifique |
| `!end` | Déverrouiller le canal, reprendre le dispatch intelligent |
| `!new` | Démarrer une nouvelle session |
| `!ask <prompt>` | Question ponctuelle |
| `!cancel` | Annuler toutes les tâches en cours |
| `!approve [tool\|reset]` | Gérer les outils auto-approuvés |
| `!status` / `!cost` / `!jobs` | Aperçu des opérations |
| `!help` | Afficher la référence des commandes |
| `@Tetora <text>` | Dispatch intelligent vers le meilleur agent |

**[Référence Complète des Commandes Discord](docs/discord-commands.md)** -- changement de modèles, bascule distant/local, configuration des fournisseurs et plus.

---

## Compiler depuis les Sources

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Cela compile le binaire et l'installe dans `~/.tetora/bin/tetora`. Assurez-vous que `~/.tetora/bin` est dans votre `PATH`.

Pour exécuter la suite de tests :

```bash
make test
```

---

## Prérequis

| Prérequis | Détails |
|---|---|
| **sqlite3** | Doit être disponible dans le `PATH`. Utilisé pour tout le stockage persistant. |
| **Clé API de fournisseur IA** | Au moins une : Claude API, OpenAI, Gemini ou tout endpoint compatible OpenAI. |
| **Go 1.25+** | Uniquement nécessaire pour la compilation depuis les sources. |

---

## Plateformes Supportées

| Plateforme | Architectures | Statut |
|---|---|---|
| macOS | amd64, arm64 | Stable |
| Linux | amd64, arm64 | Stable |
| Windows | amd64 | Bêta |

---

## Architecture

Toutes les données d'exécution sont stockées dans `~/.tetora/` :

```
~/.tetora/
  config.json        Configuration principale (fournisseurs, rôles, intégrations)
  jobs.json          Définitions des cron jobs
  history.db         Base de données SQLite (historique, mémoire, sessions, embeddings, ...)
  bin/               Binaire installé
  agents/            Fichiers d'âme par agent (agents/{name}/SOUL.md)
  workspace/
    rules/           Règles de gouvernance, auto-injectées dans tous les prompts d'agents
    memory/          Observations partagées, lisibles/modifiables par tout agent
    knowledge/       Documents de référence (auto-injectés jusqu'à 50 Ko)
    skills/          Procédures réutilisables, chargées par correspondance de prompts
    tasks/           Fichiers de tâches et listes de choses à faire
  runtime/
    sessions/        Fichiers de session par agent
    outputs/         Fichiers de sortie générés
    logs/            Fichiers de logs structurés
    cache/           Cache temporaire
```

La configuration utilise du JSON brut avec support des références `$ENV_VAR`, afin que les secrets n'aient jamais besoin d'être codés en dur. L'assistant de configuration (`tetora init`) génère un `config.json` fonctionnel de manière interactive.

Le rechargement à chaud est supporté : envoyez `SIGHUP` au daemon en cours d'exécution pour recharger `config.json` sans interruption de service.

---

## Workflows

Tetora intègre un moteur de workflows pour orchestrer des tâches multi-étapes et multi-agents. Définissez votre pipeline en JSON et laissez les agents collaborer automatiquement.

**[Documentation Complète des Workflows](docs/workflow.fr.md)** — types d'étapes, variables, déclencheurs, référence CLI et API.

Exemple rapide :

```bash
# Valider et importer un workflow
tetora workflow create examples/workflow-basic.json

# L'exécuter
tetora workflow run research-and-summarize --var topic="LLM safety"

# Consulter les résultats
tetora workflow status <run-id>
```

Consultez [`examples/`](examples/) pour des fichiers JSON de workflow prêts à l'emploi.

---

## Référence CLI

| Commande | Description |
|---|---|
| `tetora init` | Assistant de configuration interactif |
| `tetora doctor` | Vérifications de santé et diagnostics |
| `tetora serve` | Démarrer le daemon (chat bots + HTTP API + cron) |
| `tetora run --file tasks.json` | Distribuer des tâches depuis un fichier JSON (mode CLI) |
| `tetora dispatch "Summarize this"` | Exécuter une tâche ad-hoc via le daemon |
| `tetora route "Review code security"` | Distribution intelligente -- routage automatique vers le meilleur rôle |
| `tetora status` | Aperçu rapide du daemon, des jobs et des coûts |
| `tetora job list` | Lister tous les cron jobs |
| `tetora job trigger <name>` | Déclencher manuellement un cron job |
| `tetora role list` | Lister tous les rôles configurés |
| `tetora role show <name>` | Afficher les détails du rôle et l'aperçu de l'âme |
| `tetora history list` | Afficher l'historique d'exécution récent |
| `tetora history cost` | Afficher le résumé des coûts |
| `tetora session list` | Lister les sessions récentes |
| `tetora memory list` | Lister les entrées de mémoire de l'agent |
| `tetora knowledge list` | Lister les documents de la base de connaissances |
| `tetora skill list` | Lister les skills disponibles |
| `tetora workflow list` | Lister les workflows configurés |
| `tetora workflow run <name>` | Exécuter un workflow (passer `--var key=value` pour les variables) |
| `tetora workflow status <run-id>` | Afficher le statut d'une exécution de workflow |
| `tetora workflow export <name>` | Exporter un workflow en fichier JSON partageable |
| `tetora workflow create <file>` | Valider et importer un workflow depuis un fichier JSON |
| `tetora mcp list` | Lister les connexions aux serveurs MCP |
| `tetora budget show` | Afficher l'état du budget |
| `tetora config show` | Afficher la configuration actuelle |
| `tetora config validate` | Valider config.json |
| `tetora backup` | Créer une archive de sauvegarde |
| `tetora restore <file>` | Restaurer depuis une archive de sauvegarde |
| `tetora dashboard` | Ouvrir le tableau de bord web dans un navigateur |
| `tetora logs` | Voir les logs du daemon (`-f` pour suivre, `--json` pour la sortie structurée) |
| `tetora health` | Santé de l'exécution (daemon, workers, tableau de tâches, disque) |
| `tetora drain` | Arrêt gracieux : arrêter les nouvelles tâches, attendre les agents en cours |
| `tetora data status` | Afficher l'état de rétention des données |
| `tetora security scan` | Analyse de sécurité et ligne de base |
| `tetora prompt list` | Gérer les modèles de prompts |
| `tetora project add` | Ajouter un projet au workspace |
| `tetora guide` | Guide d'intégration interactif |
| `tetora upgrade` | Mettre à jour vers la dernière version |
| `tetora service install` | Installer en tant que service launchd (macOS) |
| `tetora completion <shell>` | Générer les complétions shell (bash, zsh, fish) |
| `tetora version` | Afficher la version |

Exécutez `tetora help` pour la référence complète des commandes.

---

## Contribuer

Les contributions sont les bienvenues. Veuillez ouvrir une issue pour discuter des changements majeurs avant de soumettre un pull request.

- **Issues** : [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Discussions** : [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

Ce projet est licencié sous AGPL-3.0, qui exige que les oeuvres dérivées et les déploiements accessibles par réseau soient également open source sous la même licence. Veuillez consulter la licence avant de contribuer.

---

## Licence

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
