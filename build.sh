#!/bin/bash

export APP_CUR_FILE=$(realpath "$BASH_SOURCE")
export APP_CUR_SCRIPT_DIR=$(dirname "$APP_CUR_FILE")
export APP_PROJECT_DIR=$APP_CUR_SCRIPT_DIR
export APP_PROJECT_BIN_DIR="$APP_CUR_SCRIPT_DIR/bin"
export APP_PROJECT_NAME="$(basename "$APP_PROJECT_DIR")"
# Auto-detect orb_opt image files directory.
# Override with ORB_AI_ORB_OPT_FILES_DIR env var if the project is in a non-standard location.
APP_ORB_OPT_FILES_DIR="${ORB_AI_ORB_OPT_FILES_DIR:-}"

if [ -z "$APP_ORB_OPT_FILES_DIR" ]; then
    APP_ORB_OPT_SEARCH_DIRS=(
        "$HOME/Documents/projects/web/orb_opt/docker/orb_opt/image_debian/files"
        "$HOME/projects/web/orb_opt/docker/orb_opt/image_debian/files"
        "$HOME/orb_opt/docker/orb_opt/image_debian/files"
    )

    for search_dir in "${APP_ORB_OPT_SEARCH_DIRS[@]}"; do
        if [ -d "$search_dir" ]; then
            APP_ORB_OPT_FILES_DIR="$search_dir"
            break
        fi
    done
fi

if [ -z "$APP_ORB_OPT_FILES_DIR" ]; then
    echo "Warning: orb_opt image files directory not found. Set ORB_AI_ORB_OPT_FILES_DIR env var."
    echo "Binary will be built but not copied to the Docker image."
fi

export APP_ORB_OPT_FILES_DIR

export GIT_COMMIT=$(git -C "$APP_PROJECT_DIR" rev-list -1 HEAD 2>/dev/null || echo "unknown")
export BUILD_DATE=$(date -u +%Y-%m-%d.%H:%M:%S)

if [ ! -d "$APP_PROJECT_BIN_DIR" ]; then
    mkdir -p "$APP_PROJECT_BIN_DIR"
fi

LDFLAGS="-s -w -X main.AppGitCommit=$GIT_COMMIT -X main.AppBuildDate=$BUILD_DATE"

build_target() {
    local target_os="$1"
    local target_arch="$2"
    local target_name="$3"
    local target_label="$4"
    local target_file="$APP_PROJECT_BIN_DIR/$target_name"

    printf "Building for %s... " "$target_label"

    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
        -o "$target_file" \
        -ldflags="$LDFLAGS" \
        . 2>&1

    local exit_status=$?

    if [ "$exit_status" = "0" ]; then
        printf "\033[32mok\033[0m\n"

        chmod 0755 "$target_file"

        return 0
    fi

    printf "\033[31merror\033[0m\n"

    return "$exit_status"
}

build_target "linux" "amd64" "$APP_PROJECT_NAME" "Linux (amd64)"
build_target "linux" "arm64" "${APP_PROJECT_NAME}_linux_arm64" "Linux (arm64)"
build_target "darwin" "amd64" "${APP_PROJECT_NAME}_mac" "macOS (amd64)"
build_target "darwin" "arm64" "${APP_PROJECT_NAME}_mac_arm64" "macOS (arm64)"
build_target "windows" "amd64" "${APP_PROJECT_NAME}_windows.exe" "Windows (amd64)"
build_target "windows" "arm64" "${APP_PROJECT_NAME}_windows_arm64.exe" "Windows (arm64)"

APP_LINUX_BINARY="$APP_PROJECT_BIN_DIR/$APP_PROJECT_NAME"

if [ -z "$APP_ORB_OPT_FILES_DIR" ]; then
    echo "Skipping Docker image copy (orb_opt files dir not found)"
    exit 0
fi

if [ ! -d "$APP_ORB_OPT_FILES_DIR" ]; then
    mkdir -p "$APP_ORB_OPT_FILES_DIR"
fi

APP_IMAGE_BINARY="$APP_ORB_OPT_FILES_DIR/orb_ai"

if [ -f "$APP_LINUX_BINARY" ]; then
    cp "$APP_LINUX_BINARY" "$APP_IMAGE_BINARY"
    COPY_EXIT_STATUS=$?

    if [ "$COPY_EXIT_STATUS" = "0" ]; then
        chmod 0755 "$APP_IMAGE_BINARY"
        echo "Copied Linux binary to $APP_IMAGE_BINARY"
    else
        echo "Failed to copy Linux binary to $APP_IMAGE_BINARY"
        exit 255
    fi
fi

# Backup project and copy binary to Dropbox (if available)
YEAR=$(date +%Y)
APP_PROJECT_BAK_DIR_EXT="$HOME/Dropbox/Business/$YEAR/Backups/go/$APP_PROJECT_NAME"

if [ -d "$HOME/Dropbox/Business" ]; then
    if [ ! -d "$APP_PROJECT_BAK_DIR_EXT" ]; then
        mkdir -p "$APP_PROJECT_BAK_DIR_EXT"
    fi

    echo "Syncing project to $APP_PROJECT_BAK_DIR_EXT"
    rsync -a "$APP_PROJECT_DIR/" "$APP_PROJECT_BAK_DIR_EXT/"

    SOFTWARE_DIR="$HOME/Dropbox/Business/$YEAR/software"

    if [ ! -d "$SOFTWARE_DIR" ]; then
        mkdir -p "$SOFTWARE_DIR"
    fi

    if [ -f "$APP_PROJECT_BIN_DIR/$APP_PROJECT_NAME" ]; then
        cp "$APP_PROJECT_BIN_DIR/$APP_PROJECT_NAME" "$SOFTWARE_DIR/"
        COPY_STATUS=$?

        if [ "$COPY_STATUS" = "0" ]; then
            echo "Copied binary to $SOFTWARE_DIR/"
        else
            echo "Failed to copy binary to $SOFTWARE_DIR/"
        fi
    fi
fi
