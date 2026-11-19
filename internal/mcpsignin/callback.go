package mcpsignin

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Callback is a token-free observed convenience, never sign-in authority.
type Callback struct {
	Server      string `json:"server"`
	Agent       string `json:"agent"`
	Port        int    `json:"port,omitempty"`
	Disposition string `json:"disposition"`
	Owner       string `json:"owner,omitempty"`
}

// PlanCallbacks consumes only verified user-scoped delivered decisions. Ports
// from workspace config, raw support bytes or native-name guesses never enter it.
func PlanCallbacks(launch *Launch, runtime string, linux, bridge bool, declared []string) []Callback {
	var rows []Callback
	if launch == nil || launch.Version != LaunchVersion {
		return rows
	}
	for _, decision := range launch.Decisions {
		row := Callback{Server: decision.Server, Agent: decision.Agent, Disposition: "not requested"}
		point := decision.Point
		if point != nil && point.NativeName != "" && point.CallbackRoute && point.CallbackPort >= 1024 && point.CallbackPort <= 65535 {
			row.Port = point.CallbackPort
			switch {
			case runtime == "host" || runtime == "macos-user":
				row.Disposition = "not needed"
			case !linux || runtime != "podman" || !bridge:
				row.Disposition = "not published on this backend"
			default:
				row.Disposition = "publication pending"
				for _, publication := range declared {
					spec := strings.Split(publication, "/")[0]
					parts := strings.Split(spec, ":")
					host := parts[0]
					if len(parts) > 1 {
						host = parts[len(parts)-2]
					}
					if port, err := strconv.Atoi(host); err == nil && port == row.Port {
						row.Disposition = "port busy"
					}
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

type CallbackLease struct {
	Version    int    `json:"version"`
	Port       int    `json:"port"`
	Launch     string `json:"launch"`
	Runtime    string `json:"runtime"`
	Generation string `json:"generation"`
}

func (l CallbackLease) Valid() bool {
	return l.Version == 1 && l.Port >= 1024 && l.Port <= 65535 && callbackKey(l.Launch) && l.Runtime == "podman" && callbackKey(l.Generation)
}

func callbackKey(value string) bool {
	if value == "" || len(value) > 160 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func CallbackLeasePath(root string, port int) string {
	return filepath.Join(root, "port-"+strconv.Itoa(port)+".json")
}

// ReadCallbackLease is a strict plain read: no mkdir, lock, bind, liveness or repair.
func ReadCallbackLease(root string, port int) (CallbackLease, error) {
	var lease CallbackLease
	err := readCallbackJSON(CallbackLeasePath(root, port), &lease)
	if err == nil && (!lease.Valid() || lease.Port != port) {
		err = os.ErrInvalid
	}
	return lease, err
}

type CallbackReceipt struct {
	Version    int        `json:"version"`
	Generation string     `json:"generation"`
	Callbacks  []Callback `json:"callbacks"`
}

func CallbackReceiptPath(root, generation string) string {
	if !callbackKey(generation) {
		return ""
	}
	return filepath.Join(root, "launch-"+generation+".json")
}

func ReadCallbackReceipt(root, generation string) (CallbackReceipt, error) {
	var receipt CallbackReceipt
	path := CallbackReceiptPath(root, generation)
	if path == "" {
		return receipt, os.ErrInvalid
	}
	err := readCallbackJSON(path, &receipt)
	if err == nil && (receipt.Version != 1 || receipt.Generation != generation) {
		err = os.ErrInvalid
	}
	if err == nil {
		seen := map[string]bool{}
		for _, row := range receipt.Callbacks {
			switch row.Disposition {
			case "owner", "publication pending", "unavailable", "port busy", "held", "not requested", "not needed", "not published on this backend":
			default:
				err = os.ErrInvalid
			}
			key := row.Server + "\x00" + row.Agent
			active := row.Disposition == "owner" || row.Disposition == "publication pending" || row.Disposition == "held"
			if row.Server == "" || row.Agent == "" || seen[key] || (row.Port != 0 && (row.Port < 1024 || row.Port > 65535)) || (row.Owner != "" && !callbackKey(row.Owner)) || (active && (row.Owner == "" || row.Port == 0)) {
				err = os.ErrInvalid
			}
			seen[key] = true
		}
	}
	return receipt, err
}

// ObserveCallback is the status projection of produced facts plus a plain lease
// read. It never promotes pending from process liveness or opens a listener.
func ObserveCallback(root string, launch *Launch, server string) Callback {
	row := Callback{Server: server, Disposition: "not requested"}
	if launch == nil {
		return row
	}
	rows := launch.Callbacks
	if launch.CallbackGeneration != "" {
		receipt, err := ReadCallbackReceipt(root, launch.CallbackGeneration)
		if err != nil {
			row.Disposition = "unavailable"
			return row
		}
		rows = receipt.Callbacks
	}
	for _, candidate := range rows {
		if candidate.Server != server || candidate.Disposition == "not requested" {
			continue
		}
		row = candidate
		if row.Disposition == "owner" || row.Disposition == "publication pending" || row.Disposition == "held" {
			lease, err := ReadCallbackLease(root, row.Port)
			if err != nil || lease.Launch != row.Owner || ((row.Disposition == "owner" || row.Disposition == "publication pending") && lease.Generation != launch.CallbackGeneration) {
				row.Disposition, row.Owner = "unavailable", ""
			}
		}
		return row
	}
	return row
}

func readCallbackJSON(path string, target any) error {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !parent.IsDir() || parent.Mode().Perm() != 0700 || !callbackFileOwned(parent) {
		return os.ErrPermission
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 || !callbackFileOwned(info) {
		return os.ErrPermission
	}
	// The opened inode must be the regular file classified above. No state is
	// created by this reader.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return os.ErrPermission
	}
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return err
	}
	if len(body) > 65536 {
		return os.ErrInvalid
	}
	if _, err := decodeUnique(body); err != nil {
		return os.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return os.ErrInvalid
	}
	return nil
}

func callbackFileOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
