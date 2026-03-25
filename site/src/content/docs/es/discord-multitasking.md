---
title: "Guía de Multitarea en Discord"
lang: "es"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Guía de Multitarea en Discord

Tetora admite conversaciones paralelas en Discord mediante **Thread + `/focus`**, donde cada thread tiene su propio session y vinculación de agente independientes.

---

## Conceptos Básicos

### Canal Principal — Session Único

Cada canal de Discord tiene solo **un session activo**, y todos los mensajes comparten el mismo contexto de conversación.

- Formato de clave del session: `discord:{channelID}`
- Los mensajes de todas las personas dentro del mismo canal entran al mismo session
- El historial de conversación se acumula continuamente hasta que lo reseteas con `!new`

### Thread — Session Independiente

Un thread de Discord puede vincularse a un agente específico mediante `/focus`, obteniendo un session completamente independiente.

- Formato de clave del session: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- Completamente aislado del session del canal principal; los contextos no se interfieren entre sí
- Cada thread puede vincularse a un agente diferente

---

## Comandos

| Comando | Ubicación | Descripción |
|---|---|---|
| `/focus <agent>` | Dentro del thread | Vincula este thread al agente especificado, creando un session independiente |
| `/unfocus` | Dentro del thread | Desvincula el agente del thread |
| `!new` | Canal principal | Archiva el session actual; el siguiente mensaje abrirá un session completamente nuevo |

---

## Flujo de Trabajo para Multitarea

### Paso 1: Crear un Thread en Discord

En el canal principal, hacer clic derecho en un mensaje → **Create Thread** (o usar la función de creación de threads de Discord).

### Paso 2: Vincular un Agente dentro del Thread

```
/focus ruri
```

Una vez vinculado exitosamente, toda la conversación dentro de este thread:
- Usará la configuración de personalidad SOUL.md de ruri
- Tendrá un historial de conversación independiente
- No afectará al session del canal principal

### Paso 3: Abrir Múltiples Threads según sea Necesario

```
#general (canal principal)              ← conversación cotidiana, 1 session
  └─ Thread: "Refactorizar módulo auth" ← /focus kokuyou → session independiente
  └─ Thread: "Escribir blog de la semana"  ← /focus kohaku  → session independiente
  └─ Thread: "Informe de análisis de competencia" ← /focus hisui   → session independiente
  └─ Thread: "Discusión de planificación del proyecto" ← /focus ruri    → session independiente
```

Cada thread es un espacio de conversación completamente aislado que puede ejecutarse simultáneamente.

---

## Notas Importantes

### TTL (Tiempo de Vida)

- La vinculación de thread expira por defecto después de **24 horas**
- Tras la expiración, el thread vuelve al modo normal (sigue la lógica de enrutamiento del canal principal)
- Se puede ajustar `threadBindings.ttlHours` en la configuración

### Límites de Concurrencia

- El máximo de concurrencia global está controlado por `maxConcurrent` (predeterminado: 8)
- Todos los canales y threads comparten este límite
- Los mensajes que superen el límite se pondrán en cola y esperarán

### Habilitar la Configuración

Asegurarse de que los thread bindings estén habilitados en la configuración:

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

### Limitaciones del Canal Principal

- El canal principal no puede usar `/focus` para crear un segundo session
- Para resetear el contexto de conversación, usar `!new`
- El envío de varios mensajes simultáneos dentro del mismo canal comparte el session, y el contexto puede interferir entre ellos

---

## Recomendaciones por Caso de Uso

| Situación | Enfoque Recomendado |
|---|---|
| Conversación casual, preguntas simples | Conversar directamente en el canal principal |
| Necesitas una discusión enfocada en un tema específico | Abrir un thread + `/focus` |
| Asignar diferentes tareas a diferentes agentes | Un thread por tarea, cada uno con `/focus` al agente correspondiente |
| El contexto de conversación es demasiado largo y quieres empezar de nuevo | Usar `!new` en el canal principal, o `/unfocus` seguido de `/focus` en el thread |
| Colaboración de múltiples personas en el mismo tema | Abrir un thread compartido, todos conversan dentro del thread |
