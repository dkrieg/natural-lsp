# Feature: LSP4IJ Template Validation & Fix

**Status:** Planned
**PRD requirements:** FR-45 (JetBrains client), FR-52 (importable LSP4IJ template), NFR-13 (minimal setup)
**Priority / phase:** P1 (post-assessment; real-user-reported — the shipped template does not import)
**Depends on:** [15](../15-editor-clients/plan.md) (editor clients)

## Summary

A real user reports the shipped LSP4IJ template (`editors/jetbrains/lsp4ij-template/template.json`) **does
not import** into the LSP4IJ "Import from custom template…" dialog. Verified root cause: **every field name
in our `template.json` is wrong** — it uses an invented schema (`serverName`, `command`, `mappings[]`,
`fileNamePatterns`) while LSP4IJ's actual template model expects `id`, `name`, `programArgs`, and
`fileTypeMappings[].fileType.patterns` / `languageId`. LSP4IJ deserializes `template.json` into that model,
finds none of its expected keys, and produces an empty/invalid definition (no command → nothing to launch;
no mappings → no file association), so the import is rejected or comes up blank. This feature rewrites the
template to LSP4IJ's real schema, validates it against LSP4IJ's documented format and its own shipped
example templates, and adds a check so the template can't silently drift out of spec again.

## The correct LSP4IJ template format (researched, sourced)

A template is a **directory**; only `template.json` is required (optional siblings: `settings.json`,
`settings.schema.json`, `clientSettings.json`, `initializationOptions.json`, `installer.json`, `README.md`).
The real `template.json` schema (confirmed against LSP4IJ's own `typescript-language-server` and `clangd`
templates):

```json
{
  "id": "natural-lsp",
  "name": "Natural LSP",
  "programArgs": {
    "default": "natural-lsp --stdio"
  },
  "fileTypeMappings": [
    {
      "fileType": {
        "name": "Natural",
        "patterns": ["*.NSP", "*.NSN", "*.NSS", "*.NSC", "*.NSM",
                     "*.NSL", "*.NSG", "*.NSA", "*.NSH", "*.NSD",
                     "*.NS4", "*.NS7", "*.NS3", "*.NS8", "*.NST"]
      },
      "languageId": "natural"
    }
  ]
}
```

Field mapping (ours → correct): `serverName`→`name` (plus a required `id`); `command` (string)→`programArgs`
(OS-keyed object with a `default`, optional `windows`); `mappings`→`fileTypeMappings`;
`fileNamePatterns`→`fileType.patterns` (nested under `fileType`, not at the mapping root). `initializationOptions`
belongs in a separate `initializationOptions.json`, not inside `template.json`.

Sources: LSP4IJ `docs/UserDefinedLanguageServer.md` and `docs/UserDefinedLanguageServerTemplate.md`; example
templates under `redhat-developer/lsp4ij` `src/main/resources/templates/lsp/` (`typescript-language-server/`,
`clangd/`).

## User stories

### Story 1 — The template imports and produces a working server (FR-45, FR-52)
**As a** JetBrains user, **I want** to import the shipped template into LSP4IJ **so that** the Natural LSP
server is configured (command + all 15 file-type patterns) without hand-typing.

**Acceptance criteria:**
- [ ] `editors/jetbrains/lsp4ij-template/template.json` is rewritten to LSP4IJ's real schema (`id`, `name`,
      `programArgs.default`, `fileTypeMappings[].fileType.patterns` + `languageId`) covering all 15 `.NSx`
      extensions mapped to language id `natural`.
- [ ] A `programArgs.windows` entry is provided (e.g. `natural-lsp.exe --stdio`) so Windows users get a
      correct launch command (the `default` covers Linux/macOS).
- [ ] Importing the directory via LSP4IJ "Import from custom template…" produces a server whose Command and
      Mappings are populated (manually verified in a JetBrains IDE — documented as a human checklist, since
      GUI import can't be automated in CI).

### Story 2 — The template can't silently drift out of spec (FR-52)
**As a** maintainer, **I want** an automated check that the shipped template matches LSP4IJ's schema **so
that** a future edit can't reintroduce invented field names.

**Acceptance criteria:**
- [ ] An automated test validates `template.json` against the required LSP4IJ shape: top-level `id` (string,
      non-empty), `name` (string), `programArgs.default` (string, non-empty), and `fileTypeMappings` (array)
      where each entry has `fileType.patterns` (non-empty string array) and a `languageId` — and asserts the
      **absence** of the old invented keys (`serverName`, `command`, `mappings`, `fileNamePatterns`).
- [ ] The test asserts the pattern set covers all 15 documented `.NSx` extensions.
- [ ] The check runs in CI (it can live in the existing `vscode-extension` Node job or a small standalone
      check; it must not require a running IDE).

### Story 3 — Docs reflect the corrected template (FR-45, NFR-13)
**As a** reader of the JetBrains setup docs, **I want** the docs to describe the real field names **so
that** the manual (Option B) path and the template (Option A) path are both accurate.

**Acceptance criteria:**
- [ ] `editors/jetbrains/README.md` and the root README JetBrains section are updated: the template's
      pre-filled fields are described with correct names; note the case-sensitive `patterns` (add lower-case
      variants if exports are lower-case) now living under `fileType.patterns`.
- [ ] If shipped, `initializationOptions.json` (`{}`) and any `settings.json` are documented as optional
      siblings.

## Out of scope / deferred
- A packaged JetBrains plugin (LSP4IJ remains the first-party path per FR-45).
- Marketplace distribution of the template.
- Automating the GUI import itself (not feasible in CI; validation is schema-level + a manual checklist).

## Notes
- The `editors/jetbrains/README.md` Option-B (manual) steps describe the GUI, not the JSON, so they remain
  largely valid — but the parenthetical claim that the template pre-fills "server name, command, mappings"
  must be reworded to the correct field names. Consider mirroring the directory layout of an LSP4IJ example
  template (e.g. `clangd/`) exactly to minimize drift.
