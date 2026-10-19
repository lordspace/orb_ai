# A model asked for runs, or the run fails — todo

Goal: orb_ai runs the model it was asked for, or fails and says why; it never swaps in another one.

- [P300] openai and groq swap a model name they do not list for their default without a word:
  `resolveModel()` returns `whisper-1` or `whisper-large-v3-turbo` for any other name (`orb_ai.go`,
  the "AAAAA" rows of `TestResolveModel` pin it). Passing the name through to the API would let the
  API reject an unknown one. Read 2026-10-19.
