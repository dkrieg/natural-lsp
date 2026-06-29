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
| `.NKR` | Resource (shared) | **Fragment** | shared resources (HTML, XML, GIF, JPEG, BMP); stored in `RES/` subdirectory |
| `.NR3` | Resource (private dialog) | **Fragment** | private resources for dialogs only; stored in `RES/` subdirectory |

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
- DDM column order: `C T L Name F Length S D` (DB is optional toggle).

## Cross-check against natls (prior-art parser-based LSP) — verified (2026-06-21)

natls's `NaturalFileType` enum maps the same 11 core extensions (NSD/NSN/NSP/NSS/NSH/NSG/NSL/NSA/NSM/
NSC/NS7) identically to the table above — independent corroboration. It does NOT yet handle `.NS4`
(class), `.NS8` (adapter), `.NST` (text), or `.NS3` (dialog) — they are TODO comments in its source
(along with an unassigned "RESOURCE" type). Even a mature hand-written parser punts on these, so they
are genuinely lower-priority for our analyzer. Two useful per-type predicates from natls source:

- **Can carry `DEFINE DATA`:** Subprogram, Program, (external) Subroutine, Helproutine, LDA, PDA, GDA,
  Function, **Map**. NOT: DDM, Copycode.
- **Can have a statement body:** Subprogram, Program, Subroutine, Helproutine, Function, Copycode, Map.
  NOT: the data areas (LDA/GDA/PDA), DDM.

### Referable name ≠ filename for `.NSS` and `.NS7` — verified (2026-06-21)

How a module is referred to (what `PERFORM` / function-call resolution keys on):
- Subprogram, DDM, LDA, PDA, GDA, Program, Copycode, Map, Helproutine → **the file base name**.
- **External Subroutine (`.NSS`)** → the identifier after `DEFINE SUBROUTINE` in the body — this CAN
  differ from the filename and CAN exceed 8 characters. (natls fixture `EXTSUB.NSS` defines
  `EXTERNAL-SUB`.)
- **Function (`.NS7`)** → the identifier after `DEFINE FUNCTION` in the body.

So to resolve `PERFORM` and function calls, the analyzer must read the `DEFINE SUBROUTINE` /
`DEFINE FUNCTION` name from the file, not trust the filename. (For all other types the filename is the
referable name.) Source: natls `NaturalProjectFileIndexer.getReferableName`; see natls-prior-art.md.

### DDM (`.NSD`) is NOT Natural source — verified (2026-06-21)

A `.NSD` file is a tabular field listing (columns `T L DB Name F Leng S D Remark`), not Natural
statements — it needs a separate, columnar parser. natls has a dedicated DDM parser
(`parsing/ddm/`). Do not run the statement extractor over `.NSD` files.

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
- DDM Editor (column order `C T L Name F Length S D`):
  https://documentation.softwareag.com/natux/9.3.2/en/webhelp/natux-webhelp/edis/ddm_use_editor.htm
- RESOURCE object type (`.NKR` shared, `.NR3` private):
  https://documentation.softwareag.com/natural/nat913unx/pg/pg_obj_resource.htm
- natls `NaturalFileType` (11-type enum, canHaveDefineData/canHaveBody):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalFileType.java
- natls `NaturalProjectFileIndexer` (referable-name derivation):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalProjectFileIndexer.java