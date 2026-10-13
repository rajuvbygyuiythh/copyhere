# apitest — funnel plan ("director")

Goal: the AI writes natural, context-aware lines; a small deterministic
**director** (Go, in `main.go`) reads the stranger's interest and steers every
reply toward the snap share. Nothing is random: mood, double lines, the ask and
the share are all decided from the stranger's messages.

## 1. Interest score (per stranger message)

| signal in the stranger's SMS | points | effect |
|---|---|---|
| horny / explicit words (horny, sex, nudes, dtf, wet, dick, pics …) | +3 | tier **HOT** at once |
| flirty words (cute, hot, babe, kiss, date, single, gf, love …) | +2 | |
| asks about me (`?`, wyd, hbu, how/where/what …) | +1 | |
| longer message (> 25 chars) | +1 | |
| emoji 😏😘😍🥵❤️🔥 | +1 | |
| "k / ok / lol" alone | 0 | |

Tier: **HOT** = any horny hit or score ≥ 6 · **WARM** = score ≥ 3 · **COLD** = rest.

## 2. When the snap is shared (bot reply number)

| tier | ask "u got snap?" | share username | config |
|---|---|---|---|
| HOT  | reply 3 | **reply 4** | `SHARE_HOT=4` |
| WARM | reply 5 | **reply 6** | `SHARE_WARM=6` |
| COLD | reply 7 | **reply 8** | `SHARE_COLD=8` |

* stranger asks for snap/insta/tg himself → share immediately
* "yes" after the ask → share immediately
* "no" after the ask → one teasing nudge → second "no" → polite bye, END
* age under 18 → fixed line "oh ur too young for me lol. bye", END, never shares
* stranger says bye/gtg before the share → short bye, END
* after the share: 1 more reply (remind to add), then "ttyl, i'm on snap", END

The share message itself is built by the tool from `SNAP_USERNAME`
(`ok fine, add me on snap 👻 %username%` …). The AI only writes the lead-in
line and is told it must NOT write any username — the model never holds it.

## 3. Step by step (what the director tells the model)

| reply # | instruction |
|---|---|
| 1 | greet back + one quick question (age / from / wyd) |
| 2 | if their age is unknown: asl style with the persona age ("f 21, u?") |
| middle | react to what they said; if they asked → answer first; then ONE short question in the current mood |
| horny msg | tease back, confident, suggestive but not explicit, hint snap is more fun, don't ask yet |
| flirty msg | flirt back, small compliment, keep them chasing |
| share − 1 | answer + "u got snap?" (two lines) |
| share | lead-in ("this app is boring, moving to snap") + username line |

Every note ends with: *never leave their question unanswered, never ask two
questions, never write a username or @.*

## 4. Moods (act rotation)

friendly · playful · lonely · flirty · fun/joke · curious · teasing — shuffled
per conversation, next mood every 2 replies. HOT tier only rotates
flirty → teasing → playful. The mood goes into the director note with a one-line
hint of how it sounds.

## 5. Double-line SMS (when it increases engagement)

* after a horny or flirty message: reaction line + question line
* the ask turn: answer line + "u got snap?" line
* the share turn: lead-in line + username line
* otherwise roughly every 3rd reply (reaction + question)
* never two double replies in a row

## 6. Persona / tone

`persona.txt`: college girl, home alone, bored, usa-college texting style
(lowercase, ≤10 words, u/ur/rn/wyd/hbu/lol/ngl/fr/lowkey, rare emoji), always
answers first, mirrors the stranger's energy. Age random 20–25, country random
from `COUNTRIES` (Europe by default — same country/age for everyone gave worse
results earlier).

## 7. Output guard (what the real bot would do)

lowercase, first 1–2 lines only, strip quotes/"you:"/list markers and
`<think>`; WARN when > 120 chars, > 14 words, two `?`, contains `@`/link/ai-talk.
In the real bot those cases fall back to a TXT line.

## 8. Not yet in the main bot

This whole director lives only in `apitest/`. After the tests look good we
decide together how it is wired into `internal/bot` (EVAChat Flow + AI layer).
