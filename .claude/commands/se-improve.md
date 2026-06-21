---
description: Run the software-engineer-expert knowledge-base improvement cycle (verify and record LSP/engineering facts and architecture decisions with sources)
argument-hint: "[optional: a topic to focus on, e.g. 'lsp protocol' or 'position encoding']"
---

Use the software-engineer-expert subagent. Your single objective this run is to improve the
software-engineering knowledge base in `.claude/knowledge/software-engineering/` — not to answer a
question or write analyzer code.

Scope: $ARGUMENTS

If a scope is given above, focus the cycle on that topic (still recording sources and updating the
index). If it is empty, do the full sweep across all topics.

1. **Load.** Read `.claude/knowledge/software-engineering/INDEX.md` and every topic file it lists.
   Build a list of all facts marked `needs-verification` or `unverified`, plus every item under
   "Open questions" and every entry under "Pending decisions" in `architecture-decisions.md`.
2. **Prioritize.** Order by impact on correctness of the server. Highest: the LSP protocol details
   that produce wrong results if misunderstood — above all the **position-encoding** behavior
   (UTF-16 vs. negotiated UTF-8), document-sync kind, and the exact capabilities to advertise per
   method (`lsp-protocol.md`). Then resolve any "Pending decision" that is now decidable, recording
   it as a dated ADR. Engineering-principles items rank lower.
3. **Verify, top to bottom.** Confirm against the source hierarchy: for LSP, the official
   specification (microsoft.github.io/language-server-protocol) is authoritative — quote the spec
   version. For project decisions, the repo's own README/PRD/CLAUDE/feature-plans are authoritative.
   For general engineering claims, ground them in recognized literature. Cross-check anything
   surprising. For decisions that depend on Go specifics, consult the Go KB
   (`.claude/knowledge/go/`) rather than re-deriving.
4. **Write back as you go.** For every item: correct/expand the topic file, record the **source URL**
   (or the internal doc, for project decisions), and set `Status: verified (today's date)`. Capture
   any decision made this run as a dated ADR entry (decision + rationale + alternatives considered).
   If you cannot confirm something, leave it `unverified` and say why — do not guess.
5. **Maintain the index.** Update each topic's overall status in INDEX.md, remove resolved open
   questions and pending decisions, add any new ones you discovered, and append a dated changelog
   entry summarizing what changed.
6. **Report.** End with a concise summary: what you verified, what you corrected, which decisions are
   now recorded as ADRs, what remains `unverified` and why, and the highest-value new open questions.

Do not stop mid-task — the improvements only persist once the write-back is complete. Do not modify
anything outside `.claude/knowledge/software-engineering/`.