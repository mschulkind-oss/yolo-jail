package hostprocesses

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// SelfCheck is the `yolo doctor` health check. Mirrors host_processes.self_check:
// resolves the config from YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc;
// prints one status line and returns the exit code (0 present/no-config,
// 1 config-path-set-but-missing).
func SelfCheck() int {
	var cfgPath string
	if env := os.Getenv("YOLO_HOST_PROCESSES_CONFIG"); env != "" {
		cfgPath = env
	} else {
		cwd, _ := os.Getwd()
		cwdCfg := filepath.Join(cwd, "yolo-jail.jsonc")
		if isFile(cwdCfg) {
			cfgPath = cwdCfg
		}
	}
	if cfgPath == "" {
		fmt.Println("OK: daemon present; no host_processes config in scope")
		return 0
	}
	if !isFile(cfgPath) {
		fmt.Println("FAIL: config not found at " + cfgPath)
		return 1
	}
	cfg := LoadConfig(cfgPath)
	if len(cfg.Visible) == 0 {
		fmt.Println("OK: config at " + cfgPath + " has no host_processes.visible entries")
		return 0
	}
	fmt.Println("OK: " + strconv.Itoa(len(cfg.Visible)) + " comms allowlisted at " + cfgPath)
	return 0
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
