package serverbot

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func Status() string {
	uptime, _ := os.ReadFile("/proc/uptime")
	fields := strings.Fields(string(uptime))
	seconds := float64(0)
	if len(fields) > 0 {
		seconds, _ = strconv.ParseFloat(fields[0], 64)
	}
	mem, _ := os.ReadFile("/proc/meminfo")
	values := map[string]float64{}
	for _, line := range strings.Split(string(mem), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			value, _ := strconv.ParseFloat(parts[1], 64)
			values[strings.TrimSuffix(parts[0], ":")] = value
		}
	}
	total, used := values["MemTotal"], values["MemTotal"]-values["MemAvailable"]
	ramPct := 0.0
	if total > 0 {
		ramPct = used / total * 100
	}
	var disk syscall.Statfs_t
	_ = syscall.Statfs("/", &disk)
	diskTotal := float64(disk.Blocks) * float64(disk.Bsize)
	diskFree := float64(disk.Bfree) * float64(disk.Bsize)
	diskUsed := diskTotal - diskFree
	diskPct := 0.0
	if diskTotal > 0 {
		diskPct = diskUsed / diskTotal * 100
	}
	load, _ := os.ReadFile("/proc/loadavg")
	loads := strings.Fields(string(load))
	for len(loads) < 3 {
		loads = append(loads, "0.00")
	}
	days := int64(seconds) / 86400
	hours := int64(seconds) % 86400 / 3600
	minutes := int64(seconds) % 3600 / 60
	return fmt.Sprintf("🟢 Домашний сервер работает\n\n⏱ Uptime: %dд %dч %dм\n🧠 RAM: %.2f / %.2f GiB (%.1f%%)\n💾 Диск /: %.1f / %.1f GiB (%.1f%%)\n⚙️ Load average: %s, %s, %s", days, hours, minutes, used/1048576, total/1048576, ramPct, diskUsed/(1<<30), diskTotal/(1<<30), diskPct, loads[0], loads[1], loads[2])
}
