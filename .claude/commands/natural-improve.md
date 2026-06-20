---
description: Run the natural-expert knowledge-base improvement cycle (verify, correct, and record Natural facts with sources)
argument-hint: "[optional: a topic or construct to focus on, e.g. 'DEFINE DATA' or 'file extensions']"
---

Use the natural-expert subagent. Your single objective this run is to improve the Natural knowledge
base in `.claude/knowledge/natural/` — not to answer a question or write analyzer code.

Scope: $ARGUMENTS

If a scope is given above, focus the cycle on that topic/construct (still recording sources and
updating the index). If it is empty, do the full sweep across all topics.

1. **Load.** Read `.claude/knowledge/natural/INDEX.md` and every topic file it lists. Build a list of
   all facts marked `needs-verification` or `unverified`, plus every item under "Open questions."
2. **Prioritize.** Order that list by impact on the LSP analyzer (call/resolution semantics and
   file-extension mapping rank highest, since wrong facts there produce wrong edges).
3. **Verify, top to bottom.** For each item, search the web and confirm against the source hierarchy
   in your instructions (official Software AG / NaturalONE docs first). Cross-check anything
   surprising against a second source. Note the dialect/mode/version each fact applies to.
4. **Write back as you go.** For every item: correct the topic file, add a minimal example, record the
   **source URL**, and set `Status: verified (today's date)`. If you genuinely cannot confirm
   something, leave it `unverified` and say why — do not guess.
5. **Maintain the index.** Update each topic's overall status in INDEX.md, remove resolved open
   questions, add any new open questions you discovered, and append a dated changelog entry
   summarizing what changed.
6. **Report.** End with a concise summary: what you verified, what you corrected, what remains
   `unverified` and why, and the highest-value new open questions.

Do not stop mid-task — the improvements only persist once the write-back is complete. Do not modify
anything outside `.claude/knowledge/natural/`.