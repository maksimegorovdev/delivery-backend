---
name: spec-workflow
description: Defines where specs, plans and task lists live under spec/ and how finished work is archived. Use when creating or updating a spec, plan or task list, when finishing a feature and archiving its tasks, or when you need to know what is currently in progress. Complements spec-driven-development, planning-and-task-breakdown and the /spec, /plan, /build commands from addyosmani/agent-skills by replacing their default file locations.
---

# Spec Workflow

## Overview

A companion skill for `addyosmani/agent-skills`. It gives spec-driven development artifacts one fixed home: everything lives under `spec/`, and the state of the work is read from where a file sits. Specs describe what is being built; tasks stay in `tasks/` while the work is in progress; finished work moves to `archive/`.

`/spec`, `/plan` and `/build` default to `SPEC.md` in the repo root and `tasks/plan.md`, `tasks/todo.md`. This skill replaces those locations. The root folder is deliberately named `spec/`: `/build auto` only recognizes a spec "under `spec/`" and only tolerates uncommitted planning files in `spec/*`, so any other root name makes it stop.

## When to Use

- Creating a spec, plan or task list and you need to pick its location and file name
- Changing the status of a spec
- Finishing a feature and taking its tasks out of the current work
- Finding out what is in progress or what has already been done

## Structure

```
spec/
├── specs/
│   └── <feature>/
│       └── spec.md
├── tasks/
│   └── <feature>/
│       ├── plan.md
│       └── todo.md
└── archive/
    └── <YYYY-MM-DD>-<feature>/
        ├── plan.md
        └── todo.md
```

- `<feature>` is kebab-case and identical in `specs/`, `tasks/` and `archive/`; the shared name links a spec to its tasks. When a request is split into modules, the module id is the feature name.
- `specs/` holds specs. They are permanent and stay after the work is done.
- `tasks/` holds the plan and tasks of work in progress. Several features can be in progress at once, each in its own folder.
- `archive/` holds the plan and tasks of finished work. The date in the name is the day of archiving.

## What Is Current

| Question | Where to look |
|---|---|
| What is specified and agreed | `spec/specs/*/spec.md`, status in the header |
| What is in progress | folders in `spec/tasks/` |
| What is done, and when | `spec/archive/` |

If `spec/tasks/` is empty, nothing is in progress.

## Spec

Path: `spec/specs/<feature>/spec.md`. The first line is `# Spec: <feature>`, followed by a header line:

```
> Status: **<status>** · Depends on: [<feature>](../<feature>/spec.md)
```

Omit "Depends on" when there are no dependencies. The content of the spec is defined by `spec-driven-development`.

| Status | Meaning |
|---|---|
| `draft` | Written, not yet agreed |
| `approved` | Explicitly approved by the user; planning and implementation may start |
| `done` | Implemented and verified; tasks are archived |

- Set `approved` only on the user's explicit agreement, given in a later turn than the one that saved the spec. Silence is not approval.
- There is no "in progress" status: a folder in `spec/tasks/` says that.
- If the spec turns out to be wrong during implementation, stop, set it back to `draft`, fix it, get approval again, then continue. Otherwise the spec stops describing the system.

### Capability map

When `spec-driven-development` Phase 0 splits a request into modules, save the approved map at `spec/specs/<initiative>/map.md` (`<initiative>` is kebab-case). Each module then gets an ordinary spec at `spec/specs/<module-id>/spec.md`, and its "Depends on" line mirrors the map. The map is the index of what exists, so keep it in sync when modules are added or finished.

## Plan and Tasks

Created from an `approved` spec with `/plan`, into `spec/tasks/<feature>/`. `planning-and-task-breakdown` forbids overwriting an incomplete plan; per-feature folders keep that rule satisfied, so a second feature never collides with the first.

`plan.md`:
```
# Implementation Plan: <feature>

> Spec: [spec.md](../../specs/<feature>/spec.md) · Tasks: [todo.md](todo.md)
```

`todo.md`:
```
# Tasks: <feature>

> Plan: [plan.md](plan.md) · Spec: [spec.md](../../specs/<feature>/spec.md)
```

- Put the shared Definition of Done once at the top of `todo.md`; tasks do not repeat it.
- A task is `## Task N: ...` with a description, acceptance criteria and verification, as `planning-and-task-breakdown` defines.
- Checkboxes `- [ ]` / `- [x]` reflect reality. Tick a task as soon as it is done, so another agent can tell from `todo.md` where the work stopped.

## Building

`/build` takes its next task from one plan, so it must work on exactly one feature at a time. Take the feature name from the user's request; if several folders exist in `spec/tasks/` and the name was not given, ask which one. For `/build auto`, the spec is `spec/specs/<feature>/spec.md` and a generated plan goes to `spec/tasks/<feature>/`, not to the repo root.

## Completion and Archiving

When all tasks are closed, the spec's criteria are met, and tests and linter are green:

1. Set the spec status to `done`.
2. Move the folder: `git mv spec/tasks/<feature> spec/archive/<YYYY-MM-DD>-<feature>`.
3. Check that the links in `plan.md` and `todo.md` still resolve (`../../specs/<feature>/spec.md` stays the same) and that `spec/tasks/<feature>` is gone.

The spec stays in `spec/specs/`; only the plan and tasks go to the archive, because they describe the process, not the result. The archive is not edited. To rework a finished feature, set the spec back to `draft`, get the changes approved, and create new `plan.md` and `todo.md` in `spec/tasks/<feature>/`.

## Project Setup

The slash commands hardcode their default paths in their own text, so a skill alone cannot reliably redirect them. `planning-and-task-breakdown` explicitly lets project rules (`AGENTS.md`, `CLAUDE.md`) change where plans and tasks go. Put this once in the project rules:

```
Specs, plans and tasks follow the spec-workflow skill: specs in spec/specs/<feature>/spec.md,
current plan and tasks in spec/tasks/<feature>/, finished ones in spec/archive/.
Do not create SPEC.md in the repo root or tasks/ in the repo root.
```

## Common Rationalizations

| Rationalization | Reality |
|---|---|
| "The tasks are done, I'll archive them later" | Finished work left in `spec/tasks/` looks current, and the next session starts from the wrong place |
| "The spec drifted from the code, I'll fix it at the end" | By the end nobody remembers what changed. Fix the drift immediately, via `draft` |
| "Approval is obvious from context" | `approved` is set only on an explicit "approved" |
| "The command says SPEC.md, so I'll write it there" | The command's path is a default. The project location is `spec/specs/<feature>/spec.md` |

## Red Flags

- A folder in `spec/tasks/` has every checkbox ticked
- A spec is `done` but its tasks are not archived
- `plan.md` exists while the spec is still `draft`
- `SPEC.md` or `tasks/` appears in the repo root
- The feature name differs between `specs/` and `tasks/` or `archive/`
- `/build` runs with several folders in `spec/tasks/` and no chosen feature

## Verification

- [ ] The spec is at `spec/specs/<feature>/spec.md` and its header status matches reality
- [ ] The plan and tasks were created only after `approved`
- [ ] Work in progress is in `spec/tasks/<feature>/`, finished work in `spec/archive/<date>-<feature>/`
- [ ] Links in `plan.md` and `todo.md` point to existing files
- [ ] Nothing was written to `SPEC.md` or `tasks/` in the repo root
