---
title: "Guide de multitâche Discord"
lang: "fr"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Guide de multitâche Discord

Tetora supporte les conversations parallèles sur Discord via **Thread + `/focus``, chaque thread disposant de sa propre session et liaison d'agent indépendantes.

---

## Concepts de base

### Canal principal — Session unique

Chaque canal Discord n'a qu'**une seule session active** ; tous les messages partagent le même contexte de conversation.

- Format de clé de session : `discord:{channelID}`
- Les messages de tous les utilisateurs dans le même canal entrent dans la même session
- L'historique de conversation s'accumule continuellement jusqu'à ce que vous le réinitialisiez avec `!new`

### Thread — Session indépendante

Un thread Discord peut être lié à un agent spécifique via `/focus`, obtenant ainsi une session entièrement indépendante.

- Format de clé de session : `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- Complètement isolée de la session du canal principal, les contextes ne s'interfèrent pas
- Chaque thread peut être lié à un agent différent

---

## Commandes

| Commande | Emplacement | Description |
|---|---|---|
| `/focus <agent>` | Dans un thread | Lie ce thread à l'agent spécifié, crée une session indépendante |
| `/unfocus` | Dans un thread | Supprime la liaison d'agent du thread |
| `!new` | Canal principal | Archive la session actuelle, le prochain message ouvrira une toute nouvelle session |

---

## Flux de travail multitâche

### Étape 1 : Créer un thread Discord

Faites un clic droit sur un message dans le canal principal → **Create Thread** (ou utilisez la fonctionnalité de création de thread de Discord).

### Étape 2 : Lier un agent dans le thread

```
/focus ruri
```

Une fois la liaison réussie, toutes les conversations dans ce thread :
- Utiliseront la configuration de personnalité SOUL.md de ruri
- Disposeront d'un historique de conversation indépendant
- N'affecteront pas la session du canal principal

### Étape 3 : Ouvrir plusieurs threads selon les besoins

```
#general (canal principal)                  ← conversations quotidiennes, 1 session
  └─ Thread : "Refactoring module auth"     ← /focus kokuyou → session indépendante
  └─ Thread : "Rédiger le blog de la semaine" ← /focus kohaku  → session indépendante
  └─ Thread : "Analyse de la concurrence"  ← /focus hisui   → session indépendante
  └─ Thread : "Discussion planification"   ← /focus ruri    → session indépendante
```

Chaque thread est un espace de conversation entièrement isolé, pouvant être utilisé simultanément.

---

## Points importants

### TTL (durée de vie)

- La liaison de thread expire par défaut après **24 heures**
- Après expiration, le thread revient en mode normal (suit la logique de routage du canal principal)
- Configurable via `threadBindings.ttlHours` dans la configuration

### Limites de concurrence

- Le nombre maximum de sessions simultanées globales est contrôlé par `maxConcurrent` (par défaut : 8)
- Tous les canaux et threads partagent cette limite
- Les messages dépassant la limite sont mis en file d'attente

### Activation de la configuration

Assurez-vous que les thread bindings sont activés dans la configuration :

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### Limitations du canal principal

- Le canal principal ne peut pas créer une deuxième session avec `/focus`
- Pour réinitialiser le contexte de conversation, utilisez `!new`
- Envoyer plusieurs messages simultanément dans le même canal partagera la session, les contextes pouvant s'interférer

---

## Recommandations par cas d'usage

| Cas d'usage | Approche recommandée |
|---|---|
| Conversation quotidienne, questions simples | Dialoguer directement dans le canal principal |
| Discussion approfondie sur un sujet | Ouvrir un thread + `/focus` |
| Tâches différentes assignées à des agents différents | Un thread par tâche, chacun avec `/focus` vers l'agent correspondant |
| Contexte de conversation trop long, repartir de zéro | `!new` dans le canal principal, `/unfocus` puis `/focus` dans un thread |
| Collaboration multi-utilisateurs sur le même sujet | Ouvrir un thread partagé, tous dialoguent dans le thread |
