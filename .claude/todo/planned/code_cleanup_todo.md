# Code cleanup — todo

Goal: the code, the docs and the build script match the project as it is now.

- [x] [P600] `build.sh` looked for orb_opt's image files dir only under `~/Documents/projects/web/orb_opt`,
  `~/projects/web/orb_opt` and `~/orb_opt`, so on a machine with orb_opt elsewhere the Linux binary
  was built but not copied unless `ORB_AI_ORB_OPT_FILES_DIR` was set. Read 2026-10-19. Fixed
  2026-10-19: `~/projects/tools/orb_opt` is searched first, and the binary goes to `files/bin/orb_ai`.
- [P600] `zzz_project/docs/readme.txt` calls the tool `orb_transcribe` throughout, with
  `ORB_TRANSCRIBE_*` settings and `orb_transcribe` binaries; the code reads `ORB_AI_*` and the build
  makes `orb_ai`. Read 2026-10-19.
- [P600] Older rule-lint hits in `orb_ai.go`: an identifier named `resolvedPaths`, a function taking
  two strings in a row, and an inline `||`/`&&` in a return. Rule linter 2026-10-19.
- [P700] `.claude/CLAUDE.md` names `orb_transcribe.go`, `orb_transcribe_test.go` and `AppVer` in
  `orb_transcribe.go`; the files are `orb_ai.go` and `orb_ai_test.go`. Read 2026-10-19.
