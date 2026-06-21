# Natural File Extensions (`.NSx`)

Filesystem object types as used by NaturalONE / SPoD (Natural for Linux/Unix/Windows, Eclipse-based
local workspace). The analyzer indexes these; each maps to a construct, and several LSP features depend
on indexing the right ones.

**Status: verified (2026-06-21)** — full source-extension table re-confirmed against the authoritative
NaturalONE editors "General Information" page (natONE912) and the object-types overview; `.NAT` confirmed
NOT an official extension; non-existent extensions (`.NSX/.NSV/.NSE/.NSK/.NSB`) confirmed absent from docs;
source/generated (`NS*` / `NG*`) distinction added.

## Full mapping (SOURCE objects — these are what the analyzer indexes)

The local-file naming rule: `<object-name>.<EXT>`, extension always UPPERCASE. Source objects use the
`NS*` family; the compiler emits a parallel *generated/cataloged* object with an `NG*` extension
(e.g. `NSP`→`NGP`). The LSP indexes the `NS*` source forms only; `NG*` are compiled artifacts.

| Extension | Object type | Standalone / Fragment | Notes for analyzer |
|-----------|-------------|-----------------------|--------------------|
| `.NSP` | Program | Standalone | `FETCH` / `RUN` target; main executable object |
| `.NSN` | Subprogram | Standalone | `CALLNAT` target |
| `.NSS` | (External) Subroutine | Standalone | `PERFORM` target when not an inline subroutine |
| `.NS7` | Function | Standalone | user-defined function (called via `name(< >)` function-call syntax) |
| `.NSC` | Copycode | **Fragment** | `INCLUDE` target; compile-time source inclusion, no `END`, may reference includer's symbols |
| `.NSM` | Map | Standalone (non-exec UI object) | `INPUT/WRITE USING MAP`; cataloged map source. Generated map = `.NGM` |
| `.NSH` | Helproutine | Standalone | help assigned to fields/maps |
| `.NST` | Text | **Fragment** (non-executable) | free text object; not compiled, no call/data edges |
| `.NS3` | Dialog | Standalone (event-driven) | GUI dialog (Natural for Windows); low priority for mainframe-leaning analyzer |
| `.NS4` | Class (NaturalX) | Standalone | object-oriented class definition |
| `.NS8` | Adapter | Standalone (generated interface) | generated interface object (Natural for Ajax / pages) |
| `.NSG` | Global data area (GDA) | **Fragment** (data, not exec) | `DEFINE DATA GLOBAL USING` target |
| `.NSL` | Local data area (LDA) | **Fragment** (data, not exec) | `DEFINE DATA LOCAL USING` target |
| `.NSA` | Parameter data area (PDA) | **Fragment** (data, not exec) | `DEFINE DATA PARAMETER USING`; callable interface |
| `.NSD` | DDM (Data Definition Module) | **Fragment** (definition) | DB view; referenced by `DEFINE DATA ... VIEW OF`, READ/FIND |

## `.NAT` — NOT a Natural source extension

`.NAT` is **NOT** an official Software AG Natural source/object extension and does not map to any Natural
construct in SPoD/Natural Studio, NaturalONE, or batch (SYSOBJH) export. It appears only on third-party
file-encyclopedia sites (file.org, file-extensions.org), where the listing is generic/unverified and does
not correspond to Software AG tooling. The analyzer should NOT treat `.NAT` as a Natural source file.

## Extensions that do NOT exist as Natural object types

Confirmed absent from official documentation (do not index, do not invent meanings):
`.NSX`, `.NSV`, `.NSE`, `.NSK`, `.NSB`. Note: **`NSF`** is the *product* abbreviation for *Natural SAF
Security*, NOT a file extension. **`.SAG`** is a binary *work-file* extension (runtime I/O), unrelated to
source objects. The only confirmed source family is the `NS*` set above; the only confirmed digit suffixes
are `NS3/NS4/NS7/NS8` (digits were used where the letter slot was already taken).

Notes:
- `.NS7` (Function) is especially relevant: functions introduce a *call edge* via function-call syntax,
  distinct from `CALLNAT`/`PERFORM`.
- `.NSD` (DDM) is the local-file representation of a DDM in NaturalONE, created/edited with the DDM Editor.
- Extensions/object types are dialect-stable across recent NaturalONE versions (8.x–9.x).

## Gotchas for the analyzer

- Object *type* must be derived from the extension, not from filename or content — the same base name
  (e.g. `CUSTOMER`) can exist as several object types in one library.
- `PERFORM` resolves to an inline subroutine first; only if none exists does it bind to a `.NSS`
  external subroutine (see calls-and-resolution.md). So a `.NSS` is not always the target.
- A `.NSC` copycode contains a *fragment*, not a complete object — it has no `END` and may reference
  symbols defined only in its includer. Do not analyze it as standalone for unresolved-symbol diagnostics.

## Sources

- NaturalONE editors "General Information" (full source object-type → extension table; NSP/NGP source vs
  generated naming): https://documentation.softwareag.com/naturalONE/natONE912/core/using/use-edis-geninfo.htm
- NaturalONE object-types overview: https://documentation.softwareag.com/naturalONE/natONE838/natov/pg/pg_obj-over.htm
- Managing Natural Objects (Natural Studio / SPoD, import from Windows Explorer; NSP/NGP):
  https://documentation.softwareag.com/natural/nat914win/using/stu-manageobjects.htm
- NaturalONE Documentation 9.1.3 (object types and file name extensions in the local file system):
  https://documentation.softwareag.com/naturalONE/natONE913/index.htm
- DDM in NaturalONE: https://documentation.softwareag.com/naturalONE/natONE913/natux/pg/pg_obj_ddm.htm
- DDM Editor: https://documentation.softwareag.com/naturalONE/natONE911/core/using/use-edis-ddm.htm