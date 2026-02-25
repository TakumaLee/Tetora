# Installer Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.es.md">Español</a> | <strong>Français</strong> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.it.md">Italiano</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## Prérequis

| Prérequis | Détails |
|---|---|
| **Système d'exploitation** | macOS, Linux ou Windows (WSL) |
| **Terminal** | N'importe quel émulateur de terminal |
| **sqlite3** | Doit être accessible dans le `PATH` |
| **Fournisseur IA** | Voir Chemin 1 ou Chemin 2 ci-dessous |

### Installer sqlite3

**macOS :**
```bash
brew install sqlite3
```

**Ubuntu / Debian :**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL :**
```bash
sudo dnf install sqlite
```

**Windows (WSL) :** Installez à l'intérieur de votre distribution WSL en utilisant les commandes Linux ci-dessus.

---

## Télécharger Tetora

Rendez-vous sur la [page Releases](https://github.com/TakumaLee/Tetora/releases/latest) et téléchargez le binaire pour votre plateforme :

| Plateforme | Fichier |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Utilisez le binaire Linux dans WSL |

**Installer le binaire :**
```bash
# Remplacez le nom du fichier par celui que vous avez téléchargé
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Assurez-vous que ~/.tetora/bin est dans votre PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # ou ~/.bashrc
source ~/.zshrc
```

**Ou utilisez l'installateur en une ligne (macOS / Linux) :**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Chemin 1 : Claude Pro (20 $/mois) — Recommandé pour les débutants

Ce chemin utilise **Claude Code CLI** comme backend IA. Un abonnement Claude Pro actif (20 $/mois sur [claude.ai](https://claude.ai)) est requis.

> **Pourquoi ce chemin ?** Pas de clés API à gérer, pas de surprises de facturation. Votre abonnement Pro couvre toute l'utilisation de Tetora via Claude Code.

> [!IMPORTANT]
> **Prérequis :** Ce chemin nécessite un abonnement Claude Pro actif (20 $/mois). Si vous n'êtes pas encore abonné, rendez-vous d'abord sur [claude.ai/upgrade](https://claude.ai/upgrade).

### Étape 1 : Installer Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Si vous n'avez pas Node.js :
- **macOS :** `brew install node`
- **Linux :** `sudo apt install nodejs npm` (Ubuntu/Debian)

Vérifier l'installation :
```bash
claude --version
```

Connectez-vous avec votre compte Claude Pro :
```bash
claude
# Suivez le flux de connexion dans le navigateur
```

### Étape 2 : Exécuter tetora init

```bash
tetora init
```

L'assistant de configuration vous guidera à travers :
1. **Choisir une langue** — sélectionnez votre langue préférée
2. **Choisir un canal de messagerie** — Telegram, Discord, Slack ou Aucun
3. **Choisir un fournisseur IA** — sélectionnez **« Claude Code CLI »**
   - L'assistant détecte automatiquement l'emplacement de votre binaire `claude`
   - Appuyez sur Entrée pour accepter le chemin détecté
4. **Choisir l'accès aux répertoires** — quels dossiers Tetora peut lire/écrire
5. **Créer votre premier rôle d'agent** — donnez-lui un nom et une personnalité

### Étape 3 : Vérifier et démarrer

```bash
# Vérifier que tout est correctement configuré
tetora doctor

# Démarrer le daemon
tetora serve
```

Ouvrir le tableau de bord web :
```bash
tetora dashboard
```

---

## Chemin 2 : Clé API

Ce chemin utilise une clé API directe. Fournisseurs supportés :

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Tout endpoint compatible OpenAI** — Ollama, LM Studio, Azure OpenAI, etc.

> **Note sur les coûts :** L'utilisation de l'API est facturée par token. Vérifiez les tarifs de votre fournisseur avant d'activer.

### Étape 1 : Obtenir votre clé API

**Claude API :**
1. Allez sur [console.anthropic.com](https://console.anthropic.com)
2. Créez un compte ou connectez-vous
3. Allez dans **API Keys** → **Create Key**
4. Copiez la clé (commence par `sk-ant-...`)

**OpenAI :**
1. Allez sur [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Cliquez sur **Create new secret key**
3. Copiez la clé (commence par `sk-...`)

**Endpoint compatible OpenAI (ex. Ollama) :**
```bash
# Démarrer un serveur Ollama local
ollama serve
# Endpoint par défaut : http://localhost:11434/v1
# Pas de clé API nécessaire pour les modèles locaux
```

### Étape 2 : Exécuter tetora init

```bash
tetora init
```

L'assistant vous guidera :
1. **Choisir une langue**
2. **Choisir un canal de messagerie**
3. **Choisir un fournisseur IA :**
   - Sélectionnez **« Claude API Key »** pour Anthropic Claude
   - Sélectionnez **« Endpoint compatible OpenAI »** pour OpenAI ou modèles locaux
4. **Entrer votre clé API** (ou l'URL d'endpoint pour les modèles locaux)
5. **Choisir l'accès aux répertoires**
6. **Créer votre premier rôle d'agent**

### Étape 3 : Vérifier et démarrer

```bash
tetora doctor
tetora serve
```

---

## Assistant de configuration web (non-ingénieurs)

Si vous préférez une interface graphique, utilisez l'assistant web :

```bash
tetora setup --web
```

Cela ouvre une fenêtre de navigateur sur `http://localhost:7474` avec un assistant en 4 étapes.

---

## Commandes utiles après installation

| Commande | Description |
|---|---|
| `tetora doctor` | Vérifications de santé — à exécuter en cas de problème |
| `tetora serve` | Démarrer le daemon (bots + API HTTP + tâches planifiées) |
| `tetora dashboard` | Ouvrir le tableau de bord web |
| `tetora status` | Aperçu rapide du statut |
| `tetora init` | Relancer l'assistant de configuration |

---

## Dépannage

### `tetora: command not found`

Assurez-vous que `~/.tetora/bin` est dans votre PATH :
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Installez sqlite3 pour votre plateforme (voir Prérequis ci-dessus).

### `tetora doctor` signale des erreurs de fournisseur

- **Chemin Claude Code CLI :** Exécutez `which claude` et mettez à jour `claudePath` dans `~/.tetora/config.json`
- **Clé API invalide :** Vérifiez votre clé dans la console de votre fournisseur
- **Modèle introuvable :** Vérifiez que le nom du modèle correspond à votre niveau d'abonnement

### Problèmes de connexion Claude Code

```bash
claude logout
claude
```

---

## Compiler depuis les sources

Requiert Go 1.25+ :

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## Prochaines étapes

- Lisez le [README](README.fr.md) pour la documentation complète des fonctionnalités
- Communauté : [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Signaler des problèmes : [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
