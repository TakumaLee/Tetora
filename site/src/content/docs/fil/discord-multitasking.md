---
title: "Gabay sa Discord Multitasking"
lang: "fil"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Gabay sa Discord Multitasking

Sinusuportahan ng Tetora sa Discord ang sabay-sabay na pag-uusap sa pamamagitan ng **Thread + `/focus`**, kung saan ang bawat thread ay may sariling independent na session at agent binding.

---

## Mga Pangunahing Konsepto

### Pangunahing Channel — Iisang Session

Ang bawat Discord channel ay mayroon lamang **isang active session**, at lahat ng mensahe ay nagbabahagi ng parehong conversation context.

- Format ng session key: `discord:{channelID}`
- Lahat ng mensahe mula sa lahat ng tao sa parehong channel ay pumapasok sa parehong session
- Ang kasaysayan ng pag-uusap ay patuloy na nag-iipon hanggang gamitin mo ang `!new` para i-reset ito

### Thread — Independent na Session

Ang isang Discord thread ay maaaring i-bind sa isang partikular na agent sa pamamagitan ng `/focus`, na nagbibigay ng ganap na independent na session.

- Format ng session key: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- Ganap na nakahiwalay mula sa session ng pangunahing channel, hindi nanghihimasok ang mga context
- Maaaring mag-bind ang bawat thread sa ibang agent

---

## Mga Command

| Command | Lokasyon | Paglalarawan |
|---|---|---|
| `/focus <agent>` | Sa loob ng thread | I-bind ang thread na ito sa tinukoy na agent, gumawa ng independent na session |
| `/unfocus` | Sa loob ng thread | Alisin ang agent binding ng thread |
| `!new` | Pangunahing channel | I-archive ang kasalukuyang session; ang susunod na mensahe ay magbubukas ng bagong session |

---

## Multitasking na Proseso ng Trabaho

### Hakbang 1: Gumawa ng Discord Thread

Sa pangunahing channel, i-right-click ang isang mensahe → **Create Thread** (o gamitin ang gumawa ng thread na feature ng Discord).

### Hakbang 2: I-bind ang Agent sa Loob ng Thread

```
/focus ruri
```

Pagkatapos matagumpay na mag-bind, lahat ng pag-uusap sa loob ng thread na ito ay:
- Gagamit ng SOUL.md na setting ng karakter ni ruri
- Magkakaroon ng independent na kasaysayan ng pag-uusap
- Hindi maaapektuhan ang session ng pangunahing channel

### Hakbang 3: Magbukas ng Maraming Thread Ayon sa Pangangailangan

```
#general (pangunahing channel)               ← pang-araw-araw na pag-uusap, 1 session
  └─ Thread: "Refactor auth module"          ← /focus kokuyou → independent na session
  └─ Thread: "Write this week's blog"        ← /focus kohaku  → independent na session
  └─ Thread: "Competitor analysis report"    ← /focus hisui   → independent na session
  └─ Thread: "Project planning discussion"   ← /focus ruri    → independent na session
```

Ang bawat thread ay isang ganap na nakahiwalay na espasyo ng pag-uusap, maaaring magpatuloy nang sabay-sabay.

---

## Mga Tala

### TTL (Time to Live)

- Ang mga thread binding ay nag-e-expire pagkatapos ng **24 na oras** bilang default
- Pagkatapos mag-expire, ang thread ay babalik sa normal na mode (susundin ang routing logic ng pangunahing channel)
- Maaaring i-adjust ang `threadBindings.ttlHours` sa config

### Limitasyon sa Concurrency

- Ang global na maximum na concurrency ay kinokontrol ng `maxConcurrent` (default 8)
- Ang lahat ng channel + thread ay nagbabahagi ng limitasyong ito
- Ang mga mensaheng lumagpas sa limitasyon ay ilalagay sa queue para hintayin

### Pag-enable ng Setting

Tiyaking pinagana ang mga thread binding sa config:

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

### Mga Limitasyon ng Pangunahing Channel

- Hindi maaaring gumamit ang pangunahing channel ng `/focus` para gumawa ng pangalawang session
- Kung kailangan mong i-reset ang conversation context, gamitin ang `!new`
- Ang sabay-sabay na pagpapadala ng maraming mensahe sa parehong channel ay magbabahagi ng session, at maaaring makagambala sa isa't isa ang mga context

---

## Mga Rekomendasyon para sa Bawat Sitwasyon

| Sitwasyon | Inirerekomendang Paraan |
|---|---|
| Pang-araw-araw na usapan, simpleng tanong-sagot | Direktang mag-usap sa pangunahing channel |
| Kailangan ng nakatuong talakayan sa isang paksa | Gumawa ng thread + `/focus` |
| Iba't ibang task na itinalaga sa iba't ibang agent | Isang thread bawat task, bawat isa ay may sariling `/focus` sa katumbas na agent |
| Masyadong mahaba ang conversation context at gusto mong magsimulang muli | Gamitin ang `!new` sa pangunahing channel; gamitin ang `/unfocus` pagkatapos `/focus` sa thread |
| Collaborative na trabaho ng maraming tao sa parehong paksa | Gumawa ng iisang shared thread, lahat ay nag-uusap sa loob ng thread |
