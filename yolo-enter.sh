#!/bin/bash
# YOLO Jail Global Entrypoint
# Usage: yolo [path]

JAIL_DIR="/home/matt/code/yolo_jail"
TARGET_PATH="${1:-$(pwd)}"

cd "$JAIL_DIR" && just run-path "$TARGET_PATH"
