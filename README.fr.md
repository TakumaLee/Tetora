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
  sessions/          Fichiers de session par agent
  knowledge/         Documents de la base de connaissances
  logs/              Fichiers de logs structurés
  outputs/           Fichiers de sortie générés
  uploads/           Stockage temporaire des uploads
  bin/               Binaire installé
```

La configuration utilise du JSON brut avec support des références `$ENV_VAR`, afin que les secrets n'aient jamais besoin d'être codés en dur. L'assistant de configuration (`tetora init`) génère un `config.json` fonctionnel de manière interactive.

Le rechargement à chaud est supporté : envoyez `SIGHUP` au daemon en cours d'exécution pour recharger `config.json` sans interruption de service.

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
| `tetora mcp list` | Lister les connexions aux serveurs MCP |
| `tetora budget show` | Afficher l'état du budget |
| `tetora config show` | Afficher la configuration actuelle |
| `tetora config validate` | Valider config.json |
| `tetora backup` | Créer une archive de sauvegarde |
| `tetora restore <file>` | Restaurer depuis une archive de sauvegarde |
| `tetora dashboard` | Ouvrir le tableau de bord web dans un navigateur |
| `tetora logs` | Voir les logs du daemon (`-f` pour suivre, `--json` pour la sortie structurée) |
| `tetora data status` | Afficher l'état de rétention des données |
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
