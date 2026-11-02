## Waiting on the host in Pi

Start `yolo wait r-<id>` (or `yolo wait <label>`) with `background_task` and
`completionPolicy: "wake"`, in the same turn as you relay the human's commands: its exit wakes you.
