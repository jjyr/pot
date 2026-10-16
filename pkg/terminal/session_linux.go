package terminal

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func killSession(pid int) {
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		end := strings.LastIndex(string(data), ") ")
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(data)[end+2:])
		if len(fields) < 4 {
			continue
		}
		session, _ := strconv.Atoi(fields[3])
		group, _ := strconv.Atoi(fields[2])
		if session == pid && group > 0 {
			syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	syscall.Kill(-pid, syscall.SIGKILL)
}
