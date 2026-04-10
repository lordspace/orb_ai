# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Workflow Rules

- Never commit automatically after making changes.
- Always wait for user review before committing.
- Use `./build.sh` for builds instead of ad-hoc `go build` commands unless debugging a specific issue.

## Coding Standards

1. Never remove functionality when refactoring.
2. Read the full implementation before changing behavior.
3. Check all call sites before extracting or renaming.
4. Prefer clean guard clauses and early returns.
5. Keep functions focused and small.
6. Avoid duplicated variable setup and duplicated calls.
7. Use blank lines between logical blocks so the flow is easy to scan.
8. Never pass inline transformations into function arguments or struct fields. Calculate into a named variable first.
9. Do not use deprecated APIs.
10. Do not use closures when a named function or method can do the job.

## Project Overview

`orb_transcribe` is a focused transcription CLI used by `orb_opt`.

Top-level flow should stay simple:
- get params
- process files
- show JSON result

## Build

Use:

```bash
./build.sh
```

The build script:
- builds cross-platform binaries into `bin/`
- copies the Linux AMD64 binary into `orb_opt/image_debian/files/`
