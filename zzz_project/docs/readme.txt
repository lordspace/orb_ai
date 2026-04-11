orb_transcribe
===============

What it is
----------
orb_transcribe is a focused transcription CLI used by orb_opt.

Top-level flow:
1. get params
2. process files
3. print one JSON result


Build
-----
Run from the project root:

    ./build.sh

What the build script does:
- builds cross-platform binaries into `bin/`
- copies the Linux AMD64 binary into:
  `/Users/user/Documents/projects/web/orb_opt/docker/orb_opt/image_debian/files/orb_transcribe`
- injects build metadata such as git commit and build date


Test
----
Run from the project root:

    go test ./...


Basic usage
-----------
Single file, default local provider:

    ./bin/orb_transcribe_darwin_arm64 --file input.mp3

Single file, explicit output:

    ./bin/orb_transcribe_darwin_arm64 --file input.mp3 --output-file output.txt

Directory mode:

    ./bin/orb_transcribe_darwin_arm64 --dir /path/to/audio --output-dir /path/to/out

OpenAI mode:

    ./bin/orb_transcribe_darwin_arm64 --provider openai --file input.mp3 --openai-api-key YOUR_KEY


Important defaults
------------------
- provider default: `local`
- if provider is empty and an OpenAI API key is present, provider becomes `openai`
- local backend default: `whispercpp`
- model default: `large-v3`
- output format default: `txt`
- language default: `auto`


Environment variables
---------------------
Only app-prefixed env vars are used.

Common examples:
- `ORB_TRANSCRIBE_PROVIDER`
- `ORB_TRANSCRIBE_MODEL`
- `ORB_TRANSCRIBE_MODEL_DIR`
- `ORB_TRANSCRIBE_MODEL_FILE`
- `ORB_TRANSCRIBE_LOCAL_BACKEND`
- `ORB_TRANSCRIBE_FFMPEG_BINARY`
- `ORB_TRANSCRIBE_PROVIDER_OPENAI_API_KEY`
- `ORB_TRANSCRIBE_PROGRESS`
- `ORB_TRANSCRIBE_DEBUG`

Provider-scoped env vars are also supported.
Example:
- `ORB_TRANSCRIBE_PROVIDER_LOCAL_MODEL`


Local transcription requirements
--------------------------------
Local mode expects:
- `whisper-cli` available
- `ffmpeg` available
- a local Whisper model file available

Typical model location inside orb_opt container:
- `/opt/apps/whispercpp/models`

Example local run with explicit model file:

    ./bin/orb_transcribe_darwin_arm64 --file input.mp3 --model-file /path/to/ggml-large-v3.bin


Output
------
The program prints one JSON result object.

Typical success shape:

    {
      "status": true,
      "msg": "",
      "data": {
        "file": "/abs/input.mp3",
        "output_file": "/abs/output.txt",
        "provider": "local",
        "language": "auto",
        "output_format": "txt",
        "model": "large-v3",
        "transcript": "...",
        "output_size": 1234
      }
    }


orb_opt integration
-------------------
After rebuilding `orb_transcribe`, rebuild the orb_opt image:

    cd /Users/user/Documents/projects/web/orb_opt/docker/orb_opt/image_debian
    ./build.sh

That image includes:
- `/usr/local/bin/orb_transcribe`
- `/usr/local/bin/whisper-cli`
- `/opt/apps/whispercpp/models`
