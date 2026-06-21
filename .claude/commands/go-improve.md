---
description: Run the go-expert knowledge-base improvement cycle (verify, correct, and record Go/ecosystem facts with sources)
argument-hint: "[optional: a topic to focus on, e.g. 'regexp' or 'lsp ecosystem']"
---

Use the go-expert subagent. Your single objective this run is to improve the Go knowledge base in
`.claude/knowledge/go/` — not to answer a question or write analyzer code.

Scope: $ARGUMENTS

If a scope is given above, focus the cycle on that topic (still recording sources and updating the
index). If it is empty, do the full sweep across all topics.

1. **Load.** Read `.claude/knowledge/go/INDEX.md` and every topic file it lists. Build a list of all
   facts marked `needs-verification` or `unverified`, plus every item under "Open questions."
2. **Prioritize.** Order that list by impact on this project. Highest: facts that shape the analyzer
   or core architecture — the `regexp`/RE2 capabilities and limits (`regexp-and-extraction.md`) and
   any dependency decision (`lsp-go-ecosystem.md`), since a wrong fact there misdirects the design.
   Then concurrency, stdlib/transport, filesystem/watching, and version/tooling.
3. **Verify, top to bottom.** For each item, confirm against the source hierarchy: official Go docs
   first (go.dev, pkg.go.dev, the Go spec and release notes) → the standard-library source/godoc →
   the maintainers' repo/docs for a third-party module (check last release & maintenance status) →
   reputable secondary sources (Go blog, recognized books/talks) → forums/blogs as corroboration
   only. Note the Go version a behavior applies to. Cross-check anything surprising against a second
   source.
4. **Write back as you go.** For every item: correct the topic file, add a minimal example where it
   helps, record the **source URL**, and set `Status: verified (today's date)`. If you genuinely
   cannot confirm something (especially a library's maintenance status or exact API), leave it
   `unverified` and say why — do not guess.
5. **Maintain the index.** Update each topic's overall status in INDEX.md, remove resolved open
   questions, add any new open questions you discovered, and append a dated changelog entry
   summarizing what changed.
6. **Flag skill drift.** The `go-development` skill (`.claude/skills/go-development/`) holds this repo's
   normative Go *conventions*, separate from the KB's verified *facts*. If anything you verified this
   run implies the skill's guidance is now stale or contradicted (a convention resting on an outdated
   fact, a recommended API/library that changed status), **call it out in the report** as a proposed
   skill update — including the file and what should change. Do **not** edit the skill here; changing a
   convention is a deliberate decision, made separately from this fact-verification cycle.
7. **Report.** End with a concise summary: what you verified, what you corrected, what remains
   `unverified` and why, the highest-value new open questions (call out any dependency decision that is
   now ready to make), and any skill-drift flags from step 6.

Do not stop mid-task — the improvements only persist once the write-back is complete. Do not modify
anything outside `.claude/knowledge/go/` (skill updates are proposed in the report, not applied here).