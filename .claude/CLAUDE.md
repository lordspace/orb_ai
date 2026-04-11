# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Workflow Rules

**IMPORTANT: User must review code before committing**
- NEVER commit automatically after making changes
- Always wait for user to review the code first
- Ask for permission before committing
- Only commit when user explicitly approves
- Use `./build.sh` for builds instead of ad-hoc `go build` commands unless debugging a specific issue

**Versioning (semantic: MAJOR.MINOR.PATCH)**
- Bump the **patch** version when enough features, fixes, or improvements accumulate
- Bump the **minor** version only for large, standalone features that fundamentally change capabilities
- Bump the **major** version only for breaking changes
- Update `AppVer` in `orb_transcribe.go`
- Do NOT bump the version automatically

## Coding Standards - READ THIS FIRST!

**CRITICAL REFACTORING RULES:**
1. **NEVER remove functionality when refactoring** - Always preserve ALL existing behavior
2. **READ BEFORE YOU REFACTOR** - Understand the COMPLETE original implementation before changing it
3. **CHECK ALL CALL SITES** - When extracting functions, verify all existing logic is preserved
4. **CLEAN AND EARLY RETURN** - Use guard clauses and early returns, avoid deep nesting
5. **DRY (Don't Repeat Yourself)** - Extract duplicated logic to shared functions
6. **Single Responsibility** - Each function should do ONE thing well
7. **No Duplication** - NEVER duplicate variable initialization or function calls
8. **SIMPLEST FIX** - Always aim for the simplest solution, don't over-engineer
9. **BLANK LINES around `if` blocks and between logical sections** - ALWAYS add a blank line before and after every `if` block, after early returns, between setup/action/result sections, and between logical code blocks. Code without blank lines between blocks is UNREADABLE. Each logical block must breathe. Exception: if a comment describes the block directly below it, keep them together
10. **Calculate into variables** - NEVER pass function calls or transformations inline as arguments. Always store computed values in a named variable first, then use the variable. This applies everywhere: struct fields, function arguments, and return values
11. **NEVER use deprecated APIs** - Find the correct modern alternative
12. **NO closures** - Never use anonymous functions that capture outer variables. Use named types and methods instead

**Variable Naming Convention:**
- Use Go convention: `Id` not `ID` in camelCase
- Use `Id` suffix for single identifiers, `Ids` for arrays and slices
- Names must be self-explanatory
- Never use `Path` as a suffix for file variables
- Struct fields and function names follow the same rule

## Project Overview

**orb_transcribe** is a focused transcription CLI used by `orb_opt`.

Top-level flow must stay simple:
1. get params
2. process files
3. show JSON result

The app should:
- fail fast when config is invalid
- keep per-file failures isolated during batch runs
- prefer smart defaults over fragile flag requirements
- keep provider and backend responsibilities clearly separated

## Build

Use:

```bash
./build.sh
```

The build script:
- builds cross-platform binaries into `bin/`
- copies the Linux AMD64 binary into `orb_opt/image_debian/files/`
- injects `AppGitCommit` and `AppBuildDate`

## Testing

Use:

```bash
go test ./...
```

Prefer:
- focused unit tests for parsing, normalization, file resolution, and result formatting
- keeping behavior deterministic for both single-file and directory mode

## Architecture

Code layout:
- `orb_transcribe.go` - production code
- `orb_transcribe_test.go` - tests
- `build.sh` - cross-platform build and image copy step

Design expectations:
- provider selection stays explicit and centralized
- backend-specific details stay behind provider and backend interfaces
- config parsing, job collection, and result output remain easy to scan
- smart defaults are resolved once, then reused
- cheap checks happen before expensive work
