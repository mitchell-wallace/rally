package monitor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GitDirtyCount returns the number of dirty files in a git repository.
func GitDirtyCount(dir string) (int, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0, nil // not a git repo or error -> 0
	}
	lines := strings.Split(string(out), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// LogLastActivity returns the time since the log file was last modified.
func LogLastActivity(logPath string) (time.Duration, error) {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0, err
	}
	return time.Since(info.ModTime()).Round(time.Second), nil
}

// GetPIDsInGroup returns all PIDs that belong to the given process group.
func GetPIDsInGroup(pgid int) ([]int, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pgidFromProc, err := readPGID(pid)
		if err != nil {
			continue
		}
		if pgidFromProc == pgid {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func readPGID(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return 0, fmt.Errorf("unexpected stat format")
	}
	// The process group is the 5th field (index 4).
	return strconv.Atoi(fields[4])
}

// CountTCPConnections counts established TCP connections from /proc/net/tcp.
func CountTCPConnections(pids []int) (int, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	if len(pids) == 0 {
		return 0, nil
	}

	socketInodes, err := socketInodesForPIDs(pids)
	if err != nil {
		return 0, err
	}
	if len(socketInodes) == 0 {
		return 0, nil
	}

	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	count := 0
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// State is the 4th field (index 3). 01 = ESTABLISHED.
		if fields[3] == "01" && len(fields) > 9 {
			if _, ok := socketInodes[fields[9]]; ok {
				count++
			}
		}
	}
	return count, nil
}

func socketInodesForPIDs(pids []int) (map[string]struct{}, error) {
	inodes := make(map[string]struct{})
	for _, pid := range pids {
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if inode != "" {
				inodes[inode] = struct{}{}
			}
		}
	}
	return inodes, nil
}

// ReadIOBytes returns the cumulative read+write bytes for a list of PIDs.
// These are storage (disk) I/O bytes only; see ReadSyscallBytes for network-inclusive counts.
func ReadIOBytes(pids []int) (uint64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	var total uint64
	for _, pid := range pids {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
		if err != nil {
			continue // PID may have exited
		}
		var rbytes, wbytes uint64
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "read_bytes:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "read_bytes:")), 10, 64)
				rbytes = v
			}
			if strings.HasPrefix(line, "write_bytes:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")), 10, 64)
				wbytes = v
			}
		}
		total += rbytes + wbytes
	}
	return total, nil
}

// ReadSyscallBytes returns the cumulative rchar+wchar for a list of PIDs.
// rchar and wchar count all bytes passed through read/write syscalls, including
// network sockets. Use this to detect whether an agent with open TCP connections
// is actually transferring data (vs. sitting idle at a rate limit).
func ReadSyscallBytes(pids []int) (uint64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	var total uint64
	for _, pid := range pids {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
		if err != nil {
			continue // PID may have exited
		}
		var rchar, wchar uint64
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "rchar:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "rchar:")), 10, 64)
				rchar = v
			}
			if strings.HasPrefix(line, "wchar:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "wchar:")), 10, 64)
				wchar = v
			}
		}
		total += rchar + wchar
	}
	return total, nil
}
