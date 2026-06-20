# Natural File Extensions (`.NSx`)

Filesystem object types as used by NaturalONE / SPoD (Natural for Linux/Unix/Windows, Eclipse-based
local workspace). The analyzer indexes these; each maps to a construct, and several LSP features depend
on indexing the right ones.

**Status: verified (2026-06-20)** — confirmed against the NaturalONE 9.1.3 object-type/extension
mapping in the official documentation.

## Full mapping

| Extension | Object type | Notes for analyzer |
|-----------|-------------|--------------------|
| `.NSP` | Program | `FETCH` / `RUN` target; main executable object |
| `.NSN` | Subprogram | `CALLNAT` target |
| `.NSS` | (External) Subroutine | `PERFORM` target when not an inline subroutine |
| `.NSC` | Copycode | `INCLUDE` target (compile-time source inclusion) |
| `.NSM` | Map | `INPUT USING MAP` / map references |
| `.NSL` | Local data area (LDA) | `DEFINE DATA LOCAL USING` target |
| `.NSG` | Global data area (GDA) | `DEFINE DATA GLOBAL USING` target |
| `.NSA` | Parameter data area (PDA) | `DEFINE DATA PARAMETER USING`; callable interface |
| `.NSH` | Helproutine | help assigned to fields/maps |
| `.NSD` | DDM (Data Definition Module) | database view definition; referenced by `DEFINE DATA ... VIEW OF`, READ/FIND |
| `.NS4` | Class (NaturalX) | object-oriented class definition |
| `.NS7` | Function | user-defined function (called via `name(< >)` function-call syntax) |
| `.NS3` | Dialog | event-driven GUI dialog (Natural for Windows) |
| `.NS8` | Adapter | generated interface object (e.g. for pages / Natural for Ajax) |
| `.NST` | Text | free text object (not executable) |

Notes:
- `.NS4`, `.NS7`, `.NS3`, `.NS8`, `.NST` were NOT in the original seed list and matter for completeness.
  `.NS7` (Function) is especially relevant: functions introduce a *call edge* via function-call syntax,
  distinct from `CALLNAT`/`PERFORM`. `.NS3` (Dialog) is largely Windows-GUI and low priority for a
  mainframe-leaning analyzer.
- `.NSD` (DDM) confirmed as the local-file representation of a DDM in NaturalONE. DDMs are created/edited
  with the DDM Editor and stored locally in the Eclipse workspace.
- Extensions/object types are dialect-stable across recent NaturalONE versions (8.x–9.x). The two-letter
  vs digit suffix is historical (digits used where a letter was already taken).

## Gotchas for the analyzer

- Object *type* must be derived from the extension, not from filename or content — the same base name
  (e.g. `CUSTOMER`) can exist as several object types in one library.
- `PERFORM` resolves to an inline subroutine first; only if none exists does it bind to a `.NSS`
  external subroutine (see calls-and-resolution.md). So a `.NSS` is not always the target.
- A `.NSC` copycode contains a *fragment*, not a complete object — it has no `END` and may reference
  symbols defined only in its includer. Do not analyze it as standalone for unresolved-symbol diagnostics.

## Sources

- NaturalONE Documentation 9.1.3 (object types and file name extensions in the local file system):
  https://documentation.softwareag.com/naturalONE/natONE913/index.htm
- DDM in NaturalONE: https://documentation.softwareag.com/naturalONE/natONE913/natux/pg/pg_obj_ddm.htm
- DDM Editor: https://documentation.softwareag.com/naturalONE/natONE911/core/using/use-edis-ddm.htm