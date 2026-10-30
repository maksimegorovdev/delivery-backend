---
name: spec-workflow
description: Defines where specs, plans and task lists live under spec/ and how finished work is archived, in the manner of OpenSpec. Use when creating or updating a spec, plan or task list, when finishing a feature and archiving it, or when you need to know what the system currently specifies and what is in progress. Complements spec-driven-development, planning-and-task-breakdown and the /spec, /plan, /build commands from addyosmani/agent-skills by replacing their default file locations.
---

# Spec Workflow

## Overview

A companion skill for `addyosmani/agent-skills`, modeled on OpenSpec: **`specs/` is the current truth about the system, `changes/` is work in progress, `changes/archive/` is history.** A change is one folder that carries its spec, plan and tasks together. When the change is done, its spec becomes the current spec and the whole folder moves to the archive. An agent reads the state of the project from where files sit, without opening them.

`/spec`, `/plan` and `/build` default to `SPEC.md` in the repo root and `tasks/plan.md`, `tasks/todo.md`. This skill replaces those locations. The root folder is deliberately named `spec/`: `/build auto` only recognizes a spec "under `spec/`" and only tolerates uncommitted planning files in `spec/*`, so any other root name makes it stop.

## When to Use

- Creating a spec, plan or task list and you need to pick its location and file name
- Changing the status of a spec
- Finishing a feature and archiving it
- Finding out what the system currently specifies, what is in progress, or what was done before

## Structure

```
spec/
├── specs/                          # CURRENT truth: completed specs
│   ├── <feature>/
│   │   └── spec.md
│   └── <initiative>/               # only when a request is split into modules
│       ├── map.md                  # living index of the initiative
│       └── <module>/
│           └── spec.md
└── changes/                        # IN PROGRESS
    ├── <feature>/
    │   ├── spec.md                 # the spec of this change (draft / approved)
    │   ├── plan.md
    │   └── todo.md
    ├── <initiative>/<module>/      # same three files
    └── archive/                    # HISTORY: finished changes
        ├── <YYYY-MM-DD>-<feature>/
        └── <initiative>/<YYYY-MM-DD>-<module>/    # spec.md, plan.md, todo.md
```

- `<feature>` is kebab-case and is the name of the change and of its spec. For a module of an initiative, the module id is the name. Module ids are unique across the project.
- `<spec-dir>` means `<feature>` or `<initiative>/<module>`. The same `<spec-dir>` identifies a spec in `specs/` and its change in `changes/`.
- The date in an archive folder is the day of archiving. Files that belong to one module (for example a catalog) sit next to its `spec.md` and are carried along with it.
- Links between files are relative to the file; every nested level adds one `../`.

## What Is Current

| Question | Where to look |
|---|---|
| What the system currently specifies | `spec/specs/` |
| What is in progress | `spec/changes/` (everything except `archive/`) |
| What was done before, and when | `spec/changes/archive/` |
| State of an initiative | `spec/specs/<initiative>/map.md` |

If `spec/changes/` has nothing but `archive/`, nothing is in progress.

## Spec

A new spec is written inside its change: `spec/changes/<spec-dir>/spec.md`. The first line is `# Spec: <feature>`, followed by a header line:

```
> Status: **<status>** · Depends on: [<dependency>](<relative path to its spec.md>)
```

Omit "Depends on" when there are no dependencies. Link a dependency to its spec in `spec/specs/` once it is complete. The content of the spec is defined by `spec-driven-development`.

| Status | Where | Meaning |
|---|---|---|
| `draft` | `changes/` | Written, not yet agreed |
| `approved` | `changes/` | Explicitly approved by the user; planning and implementation may start |
| `done` | `specs/` and the archived copy | Implemented and verified |

- Set `approved` only on the user's explicit agreement, given in a later turn than the one that saved the spec. Silence is not approval.
- There is no "in progress" status: a folder in `changes/` says that.
- If the spec turns out to be wrong during implementation, stop, set it back to `draft`, fix it, get approval again, then continue. Otherwise the spec stops describing the system.

### Capability map

When `spec-driven-development` Phase 0 splits a request into modules, save the approved map at `spec/specs/<initiative>/map.md` (`<initiative>` is kebab-case), instead of the project root and `SPEC-<id>.md` names that Phase 0 suggests. The map lists every module with its status and a link to its spec: to `changes/<initiative>/<module>/spec.md` while the module is in progress, to `specs/<initiative>/<module>/spec.md` once it is complete. Keep it in sync whenever a module is added, approved or completed.

## Plan and Tasks

Created from an `approved` spec with `/plan`, next to the spec in `spec/changes/<spec-dir>/`. `planning-and-task-breakdown` forbids overwriting an incomplete plan; one folder per change keeps that rule satisfied, so a second change never collides with the first.

`plan.md`:
```
# Implementation Plan: <feature>

> Spec: [spec.md](spec.md) · Tasks: [todo.md](todo.md)
```

`todo.md`:
```
# Tasks: <feature>

> Plan: [plan.md](plan.md) · Spec: [spec.md](spec.md)
```

- Put the shared Definition of Done once at the top of `todo.md`; tasks do not repeat it.
- A task is `## Task N: ...` with a description, acceptance criteria and verification, as `planning-and-task-breakdown` defines.
- Checkboxes `- [ ]` / `- [x]` reflect reality. Tick a task as soon as it is done, so another agent can tell from `todo.md` where the work stopped.

## Building

`/build` takes its next task from one plan, so it must work on exactly one change at a time. Take the name from the user's request; if several folders exist in `spec/changes/` and the name was not given, ask which one. For `/build auto`, the spec is `spec/changes/<spec-dir>/spec.md` and a generated plan goes next to it, not to the repo root.

## Completion and Archiving

When all tasks are closed, the spec's criteria are met, and tests and linter are green:

1. Set the spec status to `done`.
2. **Publish the spec:** copy `spec/changes/<spec-dir>/spec.md` (and the module's own files, such as a catalog) to `spec/specs/<spec-dir>/`. If a spec is already there, this replaces it: `specs/` always holds the latest completed version.
3. **Archive the change:** `git mv spec/changes/<spec-dir> spec/changes/archive/<archive-dir>`, where `<archive-dir>` is `<YYYY-MM-DD>-<feature>` or `<initiative>/<YYYY-MM-DD>-<module>` (create `archive/<initiative>/` first). The archived `spec.md` stays as the snapshot of what was agreed for this change.
4. Fix links: the module's row and status in `map.md` now point to `specs/`, "Depends on" in other specs point to the published spec, and the map link in the archived `plan.md` and `todo.md` keeps resolving.
5. Check that all links in `spec/` resolve and that nothing but `archive/` is left in `spec/changes/` for this change.

The archive is not edited afterwards, except to keep links valid. To rework a completed feature, start a new change with the same `<spec-dir>`: copy the current spec from `specs/` into `changes/<spec-dir>/spec.md` as `draft`, change it, get it approved, plan, build, complete. Completion replaces the spec in `specs/`; the old change stays in the archive under its own date.

## Project Setup

The slash commands and `planning-and-task-breakdown` hardcode their default paths (`planning-and-task-breakdown` only lets project rules swap the task list for an external tracker, and has no hook for moving `plan.md`). So a skill alone cannot reliably redirect them. A project rule in `AGENTS.md` or `CLAUDE.md` is an explicit instruction from the user and wins over those defaults. Put this once in the project rules:

```
Specs, plans and tasks follow the spec-workflow skill: completed specs in spec/specs/<spec-dir>/,
changes in progress (spec, plan, tasks) in spec/changes/<spec-dir>/, history in spec/changes/archive/.
Do not create SPEC.md in the repo root or tasks/ in the repo root.
```

## Common Rationalizations

| Rationalization | Reality |
|---|---|
| "The tasks are done, I'll archive the change later" | A finished change left in `spec/changes/` looks like current work, and the next session starts from the wrong place |
| "I'll skip publishing the spec, the archive has it" | `specs/` is what the next change builds on. If it is stale, the system's current behavior is documented nowhere |
| "The spec drifted from the code, I'll fix it at the end" | By the end nobody remembers what changed. Fix the drift immediately, via `draft` |
| "Approval is obvious from context" | `approved` is set only on an explicit "approved" |
| "The command says SPEC.md, so I'll write it there" | The command's path is a default. The project location is `spec/changes/<spec-dir>/spec.md` |

## Red Flags

- A folder in `spec/changes/` has every checkbox ticked
- A spec with status `done` in `spec/changes/` outside the archive, or a `draft`/`approved` spec in `spec/specs/`
- A change in the archive whose spec is not in `spec/specs/`
- `plan.md` exists while the spec is still `draft`
- `SPEC.md` or `tasks/` appears in the repo root
- A link in `map.md` or "Depends on" that points to a moved path
- `/build` runs with several folders in `spec/changes/` and no chosen change

## Verification

- [ ] The new spec was written in `spec/changes/<spec-dir>/spec.md` and its header status matches reality
- [ ] The plan and tasks were created only after `approved`
- [ ] On completion: the spec is published in `spec/specs/<spec-dir>/` and the change folder is in `spec/changes/archive/`
- [ ] Links in the map, other specs and the archived files resolve
- [ ] Nothing was written to `SPEC.md` or `tasks/` in the repo root
