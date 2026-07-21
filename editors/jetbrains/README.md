# natural-lsp — JetBrains integration

Run `natural-lsp` in **any** JetBrains IDE — including the free **Community**
editions (IntelliJ IDEA CE, PyCharm CE, etc.) — via the free
[**LSP4IJ**](https://github.com/redhat-developer/lsp4ij) plugin.

JetBrains' own native LSP API (`com.intellij.platform.lsp`) is only available in
the paid/Ultimate-tier IDEs and requires a custom plugin. LSP4IJ is a
general-purpose LSP client that works everywhere, which is why it is the
first-party path here: it needs no bespoke plugin and no paid IDE.

The server is filesystem-scoped and finds its workspace root by walking up from
the opened file for a `.natural-lsp.toml` sentinel (see the root README's
"Workspace configuration"), so open the **project root** that contains that file.

## Prerequisites

- A JetBrains IDE 2023.2 or newer (Community or Ultimate).
- The `natural-lsp` binary on your `PATH`. Verify with:

  ```bash
  natural-lsp --version
  ```

  (See the root [README](../../README.md) → Installation for pre-built binaries,
  build-from-source, and `go install`.)

## Setup

### Option A — import the ready-made template (reproducible)

This repository ships an importable LSP4IJ **user-defined language-server
template** under [`lsp4ij-template/`](./lsp4ij-template/). Importing it pre-fills
the server `id`/`name` (`natural-lsp` / `Natural LSP`), the `programArgs` launch
command (`natural-lsp --stdio`, with a `windows` variant `natural-lsp.exe --stdio`),
and the 15 `fileType.patterns` (`*.NSP … *.NST`) all mapped to language id `natural`
— so the setup is reproducible instead of hand-typed. No
`initializationOptions.json` or `settings.json` sibling is shipped because no
initialization options are currently needed; add one if a real option arises.

1. Install **LSP4IJ** from the JetBrains Marketplace
   (*Settings/Preferences → Plugins → Marketplace → search "LSP4IJ"*), then
   restart the IDE.
2. Open **Settings/Preferences → Languages & Frameworks → Language Servers**.
3. Click **[+] → Template → Import from custom template…** and select this
   repo's `editors/jetbrains/lsp4ij-template/` directory.
4. Confirm the **Command** shows `natural-lsp --stdio` (Windows: `natural-lsp.exe
   --stdio`) and the **Mappings** tab lists the 15 file-name patterns below, all
   mapped to language id `natural`. Click **OK / Apply**.

### Option B — configure by hand

If you cannot point the import dialog at this repo, create the server manually:

1. Install **LSP4IJ** (as above).
2. **Settings/Preferences → Languages & Frameworks → Language Servers → [+] → New Language Server**.
3. On the **Server** tab set:
   - **Server name:** `Natural LSP`
   - **Command:** `natural-lsp --stdio`
4. On the **Mappings** tab, add a **File name pattern** row for each of the 15
   Natural extensions, each with **Language ID** `natural`:

   ```
   *.NSP  *.NSN  *.NSS  *.NSC  *.NSM  *.NSL  *.NSG  *.NSA
   *.NSH  *.NSD  *.NS4  *.NS7  *.NS3  *.NS8  *.NST
   ```

   File-name patterns are used (rather than a custom IntelliJ file type) because
   Community editions do not register a Natural file type — patterns work in
   every edition. Note LSP4IJ patterns are **case-sensitive**; if your exports
   use lower-case suffixes, add the lower-case variants (e.g. `*.nsp`) in the
   **Mappings** tab — or, if using the template, add them under `fileType.patterns`
   in `lsp4ij-template/template.json` before importing.
5. Click **OK / Apply**.

## Verify

Full GUI verification requires a running JetBrains IDE, so this checklist is a
**manual (human) step** — it cannot be automated in CI here. The automatable
lower bound is the server-side stdio smoke, which you can run first:

```bash
# from the natural-lsp repo root, or against an installed binary:
scripts/smoke.sh "$(command -v natural-lsp)"
```

Then, in the IDE, using this repo's sample workspace
(`docs/plans/features/15-editor-clients/sample-workspace/`):

**Post-import checks (manual — GUI import cannot be automated in CI):**

- [ ] After importing the template (Option A, step 3), confirm that the **Command**
      field is populated with `natural-lsp --stdio` (or `natural-lsp.exe --stdio` on
      Windows) and that the **Mappings** tab lists all 15 `*.NSx` patterns mapped to
      language id `natural`. This verifies the `programArgs` and `fileTypeMappings`
      fields were imported correctly from `template.json`.

**Server smoke-test:**

- [ ] Open the sample-workspace directory as the project (it contains the
      `.natural-lsp.toml` sentinel).
- [ ] Open `HELLO.NSP`. The **LSP4IJ console** (View → Tool Windows → *Language
      Servers*, or the LSP console) shows the `Natural LSP` server transition to
      **started / connected**.
- [ ] Put the caret on `CALLGREET` in `CALLNAT 'CALLGREET'` and invoke
      **Go to Declaration/Definition** (Ctrl/Cmd-B) — it navigates to
      `CALLGREET.NSN`.
- [ ] Put the caret on `SAYHELLO` in `PERFORM SAYHELLO` and Go to Definition —
      it navigates to `SAYHELLO.NSS`.

If the server does not start, confirm `natural-lsp` is on the IDE process's
`PATH` (the IDE may not inherit a shell-only `PATH`; use an absolute command
such as `/usr/local/bin/natural-lsp --stdio` if needed).

## See also

- Root [README](../../README.md) → Editor setup → JetBrains IDEs (points here).
- `docs/plans/natural-lsp-prd.md` (FR-45).
