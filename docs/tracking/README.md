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

## What git tracks in `docs/`

Since commit `36c1720` the `docs/` folder itself is no longer ignored, so the tracker files here (`tracker.json`, the generated `SELA_Progress_Tracker.xlsx` and CSVs, `usability_sessions.csv`, this `README.md`) are versioned. The specs (`FSD_Sela.md`, `PRD_Sela.md`, `BRD_Sela.md` and their PDFs) are still excluded by the repository-wide `*.md` and `*.pdf` rules, so they are **not** in commits or backups made from git. Back them up separately, or add `!docs/*.md` and `!docs/*.pdf` to `.gitignore` if you want them versioned too.

## Timed usability check (T7.2)

The PRD success metric is: a host finishes event setup in **3 minutes or less, with no documentation**, measured as the median of **3 to 5 first-time hosts**. Automation cannot be a first-time host, so the two are recorded separately:

- **Automated (T7.1, done).** `apps/web/tests/e2e/host-happy-path.spec.ts` times a scripted host through sign-in, category, form and event creation and writes `apps/web/test-results/timing.json`. It is a lower bound (a script never hesitates, reads or mistypes), not the metric.
- **Human sessions (T7.2, open).** Record each session as one row of `usability_sessions.csv`, then put the median in the tracker and the PRD metrics table.

Protocol for each session:

1. Participant has never used Sela and gets no instructions beyond "create an event for your next occasion". Do not point at buttons; note every time you had to help (`help_needed`).
2. Phone-size Chrome (or a phone), app opened at `/`. Start the clock when the page is first shown.
3. In development the sign-in email lands in Mailpit (`http://127.0.0.1:8025`), so a facilitator has to read the code out. Record that wait as `otp_wait_s` and report the total both with and without it. With a real mail provider this wait would be the participant's own inbox lookup.
4. Stop the clock when the event page shows the short link and QR code. Record `login_s` (open to signed in), `create_s` (signed in to result screen) and `total_s`.
5. Use 3 to 5 different participants; the median of `total_s` is compared with 180 s. Do not reuse a participant, and never edit a row after the fact; add a note instead.

The metric is met only when the median of at least 3 sessions is at or under 180 s. Until rows exist, T7.2 stays *Pending Verification* and Milestone 1 is complete except for this item.
