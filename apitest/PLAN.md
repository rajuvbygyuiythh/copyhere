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

## 4. Moods (act rotation — context first, rotation as fallback)

| what the stranger just did | mood used |
|---|---|
| horny | teasing ↔ flirty (alternating) |
| flirty / asks for a pic | flirty |
| laughs (lol, haha, 😂) | fun/joke |
| says bored / alone / cant sleep | lonely (relate: home alone too) |
| 2+ one-word answers in a row | lonely → teasing (hook) |
| tells something about themselves | curious |
| nothing special | shuffled rotation friendly · playful · lonely · flirty · fun/joke · curious · teasing, next mood every 2 replies (HOT: flirty → teasing → playful) |

The mood goes into the director note with a one-line hint of how it sounds.

## 5. Double-line SMS (only when it buys engagement)

| situation | lines | why |
|---|---|---|
| after a horny / flirty message | 2 | reaction + hook |
| 2+ one-word answers in a row | 2 | tease + effortless question = restart |
| they wrote a long message about themselves (> 40 chars) | 2 | they invested → reward |
| the ask turn | 2 | answer + "u got snap?" |
| the share turn | 2 | lead-in + username |
| they wrote one word ("k", "lol") | 1 | mirror their energy |
| pic request | 1 | short "not here lol" |
| otherwise | every 3rd reply | rhythm |

Never two double replies in a row, never on reply 1.

## 5b. Special situations the director handles

| stranger | bot |
|---|---|
| "r u a bot / real?" | laugh it off in 3–6 words, flip it back with one question |
| "send pic" | "not on this app lol, only on snap" — no snap ask yet (+2 interest); after the ask it counts as a yes → share |
| "idk / maybe / why" after the ask | one reassure line (easier to talk there) → share on the next reply anyway |
| tier jumps late (WARM at reply 6) | ask now, share next reply; HOT shares right away |
| one-word streak | 4 rotating hooks: tease for being quiet / tiny thing about my night / either-or question / "ur boring me a lil" |
| after the share: "whats ur snap again" | username line sent again |
| after the share: "added" | "ok see u there", END |

## 5c. Reply quality check (one retry)

After the model answers, the tool checks the lines and asks the model ONCE
more with the reason when:

* the stranger asked a question and the reply only asks back (pending answer)
* the reply repeats an earlier bot line
* the reply contains `@` / a link / ai-talk

Printed as `RETRY: <reason>` with the first draft, so you can see how often the
model needs it. The status line shows the plan reason (`opener`, `horny`,
`ask snap`, `cold streak -> hook`, `share turn` …) for every reply.

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
