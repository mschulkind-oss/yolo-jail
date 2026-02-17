# Tasks: Podman Runtime Support

**RFC:** [002_podman-runtime.md](../plans/002_podman-runtime.md)

## Phase 1: Core Runtime Abstraction

- [ ] Add `_runtime()` function to cli.py with env/config/auto-detect logic
- [ ] Add `import shutil` to cli.py
- [ ] Thread `runtime` parameter through all functions that call container commands
- [ ] Replace `"docker"` in `find_running_container()` with runtime param
- [ ] Replace `"docker"` in `auto_load_image()` with runtime param  
- [ ] Replace `"docker"` in exec path (container reuse) with runtime
- [ ] Replace `"docker"` in `run` path (new container) with runtime
- [ ] Replace `"docker"` in `ps()` command with runtime
- [ ] Update all error/status messages to use runtime name
- [ ] Update Justfile to support podman

## Phase 2: Testing & Validation

- [ ] Run existing tests (ensure nothing breaks)
- [ ] Manual test: `YOLO_RUNTIME=podman yolo -- echo hello`
- [ ] Manual test: `YOLO_RUNTIME=docker yolo -- echo hello` (if docker available)
- [ ] Manual test: auto-detect (unset YOLO_RUNTIME)
- [ ] Manual test: container reuse (exec into running container)
- [ ] Manual test: `yolo ps`

## Phase 3: Documentation

- [ ] Update AGENTS.md with podman support info
- [ ] Update docs/USER_GUIDE.md if it references docker
- [ ] Commit and push
