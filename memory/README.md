# memory/ — living project memory (read this folder FIRST in every new session)

Purpose: if the agent's context is lost (token limit, new session, sandbox
reset), everything needed to continue is here. The owner should never have
to re-explain the project.

## Reading order (≈5 minutes)

| # | File | What it gives you |
|---|------|-------------------|
| 1 | `00_RESUME_BRIEF.md` | **Current state, last thing done, open decisions, exact next steps.** Always current. |
| 2 | `02_RULES_AND_DECISIONS.md` | Owner's standing rules (hard rules first) + every product decision with its reason. |
| 3 | `04_ACTIVE_PLANS.md` | Approved / proposed plans with status, targeted files, acceptance criteria. |
| 4 | `01_PROJECT_MAP.md` | Repo map: what lives where, which files are "ours" vs original. |
| 5 | `05_ENV_RECIPES.md` | Sandbox + Windows build/test/preview recipes, known dead ends (do not retry). |
| 6 | `03_SESSION_LOG.md` | Chronological log: commits, what changed, why (append-only). |
| 7 | `06_CHAT_HIGHLIGHTS.md` | Condensed chat history: the owner's key messages (Banglish) + the agent's answer, per turn. |

Deeper docs (unchanged conventions): root `AGENTS.md` (scope + hard rules),
`apitest/HANDOFF.md` + `apitest/PLAN.md` (AI funnel lab), `docs/*.md`
(engine mapping, pacing, live wall).

## Update protocol (agent MUST follow, every turn)

1. **End of every turn** that changed code, plans or decisions: update
   `00_RESUME_BRIEF.md` (state + next steps), append to `03_SESSION_LOG.md`
   and `06_CHAT_HIGHLIGHTS.md`; touch `02_*`/`04_*`/`01_*` when a rule,
   decision, plan or file layout changed.
2. Commit the memory files **with** the code change (same push) so the
   branch on GitHub is always resumable: `git push origin arena/30e64ce8-copyhere`.
3. Never put secrets here (API keys, snap usernames, sessions, cookies).
   Describe where they live, never their values.
4. Keep `00_RESUME_BRIEF.md` short and current; move history to the log.
5. Write memory in English (dense, for agents). Talk to the owner in Bengali.

## মালিকের জন্য (Bengali note)

এই ফোল্ডারটা প্রজেক্টের "স্মৃতি"। নতুন session-এ agent-কে শুধু বলবেন:
**"memory/00_RESUME_BRIEF.md পড়ে continue করো"** — বাকিটা সে নিজেই বুঝে নেবে।
প্রতিটা turn-এর শেষে agent এই ফাইলগুলো নিজে update করে commit/push করবে।
