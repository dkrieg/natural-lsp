# Natural LSP — LSP4IJ template

Importable [LSP4IJ](https://github.com/redhat-developer/lsp4ij) user-defined
language-server template for [`natural-lsp`](https://github.com/dkrieg/natural-lsp).

Prerequisite: the `natural-lsp` binary must be on your `PATH` (see the root
README's Installation section). The template launches it as `natural-lsp --stdio`.

## Import

In a JetBrains IDE with LSP4IJ installed:

1. **Settings / Preferences → Languages & Frameworks → Language Servers**.
2. Click **[+] → Template → Import from custom template…**.
3. Select this `lsp4ij-template/` directory.

The server name, command, and the 15 Natural file-name-pattern mappings
(`*.NSP … *.NST`, all mapped to language id `natural`) are pre-filled. Apply,
then open a `.NSP` file in a project that contains a `.natural-lsp.toml`
sentinel at its root.

If your exported objects use lower-case extensions, add the lower-case patterns
(e.g. `*.nsp`) to the mappings — LSP4IJ file-name patterns are case-sensitive.
