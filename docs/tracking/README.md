# SELA progress tracking and docs maintenance

This folder holds the project's progress tracker and the process for keeping it, the change log and the specifications in `docs/` in step.

## Files

| File | Role |
|------|------|
| `tracker.json` | **Source of truth.** Tasks, change log entries and per-requirement status. Edit this file only. |
| `SELA_Progress_Tracker.xlsx` | Generated workbook: `Guide`, `Summary` (live counts), `Tracker`, `Change Log`, `Traceability`. Do not edit by hand. |
| `tracker.csv`, `changelog.csv`, `traceability.csv` | Generated plain-text mirrors (Excel-importable, easy to diff). |
| `../../scripts/build_tracker.py` | Validates `tracker.json` and regenerates the workbook and CSVs. |

Regenerate with `python scripts/build_tracker.py` from the repository root (needs `openpyxl`). `--check` validates without writing. The script fails, and writes nothing, when:

- a commit hash in the data does not exist in git;
- a task is `Completed` without a GREEN commit (or an explicit `n/a (reason)`) or without a change log entry;
- a status is not one of the allowed values;
- an FR ID or task reference does not exist.

Requirement text on the `Traceability` sheet is parsed from `docs/FSD_Sela.md`, so it can never drift from the specification.

## Statuses

**Tasks:** `Not Started`, `In Progress` (RED committed or work under way), `Pending Verification` (implemented, a stated verification step is still open), `Completed`.
**Requirements:** `Not Started`, `Partial`, `Completed`.

A task is `Completed` only when all of these hold:

1. RED and GREEN commits are recorded (refactors and tooling may state `n/a` with the reason).
2. Coverage is at least 80% for packages with logic.
3. `bash scripts/check.sh` passes.
4. The docs are synced (below) and the change log entry exists.

## Process when a task is finished

1. **Tracker.** In `tracker.json`, set the task's `status`, `red` and `green` commit hashes (`git log --oneline`), `coverage` (`go test -cover` or `npm run test:coverage`) and `updated`. Add new task rows for follow-up work you discovered.
2. **Change log.** Add an entry under `changelog` with: what was implemented; breaking or structural changes (moved directories, new migrations, new required environment variables or tools); deviations and technical decisions with the reason.
3. **Requirements.** For every FR ID the task touched, update its entry under `requirements` (`status`, `tasks`, `evidence`). Use `Partial` until every part of the requirement exists.
4. **Specs.** Update the documents as described below.
5. **Build.** Run `python scripts/build_tracker.py` and fix anything it reports.
6. **Commit** the code and tests. Put the FR IDs in the commit body so `git log --grep "FR-01"` finds them.

## Keeping `docs/` in step

| Change | Update |
|--------|--------|
| Architecture, repo layout, transport (REST/gRPC), tooling | FSD 2.x, mainly 2.6 |
| Tables, columns, constraints, seed data | FSD 5 (implementation notes) |
| Endpoints, gRPC services, error mapping | FSD 6 |
| Auth, sessions, encryption, rate limits | FSD 8 and the matching FR-SEC rows |
| Requirement status | FSD 11.1 (keep it consistent with the `Traceability` sheet) |
| Any of the above, once per batch | FSD header: bump the version and add a "Perubahan dari vX" row |
| Product scope, pricing, success metrics, personas | PRD or BRD, **only** when scope actually changes |

Rules:

- Update a document in the same change that introduces the difference. Record deviations from the spec explicitly (say what the spec said and what was built), never silently.
- Never resolve a documented inconsistency by editing around it. The open ones are: MVP category count (BRD 4.2 vs 10), free-tier watermark (PRD vs BRD 7), BR-17/BR-18 numbering (BRD 4.3 vs 6), and the stale "v2.1" wording in FSD 1 and 12. Decide with the product owner, then fix all affected files together.
- Do not invent values the documents leave undefined; store them as `tbd` (see the category defaults) and list the open question.

## Caveat: `docs/` is not tracked by git

The repository's `.gitignore` excludes `docs/`, so this folder, the FSD updates and the generated files are not in commits or backups made from git. `scripts/build_tracker.py` is tracked, but `tracker.json` is not. Either back `docs/` up separately, or change `.gitignore` (for example `docs/*` followed by `!docs/tracking/` and `!docs/*.md`) if you want the tracker and specs versioned.
