// Package procfs provides zero-allocation, stack-buffered parsers for Linux
// /proc/stat, /proc/loadavg, and /proc/meminfo virtual files.
package procfs

import (
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

// CPUTracker tracks previous /proc/stat CPU jiffies to compute accurate delta CPU utilization percentages.
type CPUTracker struct {
	mu        sync.Mutex
	prevTotal uint64
	prevIdle  uint64
	hasPrev   bool
}

// NewCPUTracker constructs a new CPUTracker.
func NewCPUTracker() *CPUTracker {
	return &CPUTracker{}
}

// SampleCPUPercent reads the first line of /proc/stat using a fixed stack buffer
// and calculates the percentage of active CPU time since the last sample.
func (c *CPUTracker) SampleCPUPercent(procStatPath string) float64 {
	total, idle, ok := ReadProcStatTicks(procStatPath)
	if !ok {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.hasPrev || total <= c.prevTotal {
		c.prevTotal = total
		c.prevIdle = idle
		c.hasPrev = true
		return 0
	}

	deltaTotal := float64(total - c.prevTotal)
	deltaIdle := float64(idle - c.prevIdle)
	c.prevTotal = total
	c.prevIdle = idle

	if deltaTotal <= 0 {
		return 0
	}
	busyPct := ((deltaTotal - deltaIdle) / deltaTotal) * 100.0
	if busyPct < 0 {
		busyPct = 0
	}
	if busyPct > 100 {
		busyPct = 100
	}
	return round2(busyPct)
}

// readFirstBytes reads at most len(buf) bytes from a virtual /proc file without
// allocating heap buffers or reading trailing kernel lines.
func readFirstBytes(path string, buf []byte) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	n, _ := f.Read(buf)
	_ = f.Close()
	return n
}

// ReadProcStatTicks parses the aggregate "cpu " first line from /proc/stat using a stack buffer.
func ReadProcStatTicks(path string) (total uint64, idle uint64, ok bool) {
	var buf [256]byte
	n := readFirstBytes(path, buf[:])
	if n < 6 {
		return 0, 0, false
	}

	data := buf[:n]
	for i, b := range data {
		if b == '\n' {
			data = data[:i]
			break
		}
	}
	if len(data) < 5 || data[0] != 'c' || data[1] != 'p' || data[2] != 'u' || data[3] != ' ' {
		return 0, 0, false
	}

	fieldIdx := 0
	i := 4
	for i < len(data) {
		for i < len(data) && data[i] == ' ' {
			i++
		}
		if i >= len(data) {
			break
		}
		var val uint64
		hasDigit := false
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			val = val*10 + uint64(data[i]-'0')
			hasDigit = true
			i++
		}
		if hasDigit {
			total += val
			// Field 3 is idle, field 4 is iowait
			if fieldIdx == 3 || fieldIdx == 4 {
				idle += val
			}
			fieldIdx++
		} else {
			for i < len(data) && data[i] != ' ' {
				i++
			}
		}
	}

	if fieldIdx < 4 {
		return 0, 0, false
	}
	return total, idle, true
}

// ReadLoadAverages parses 1m, 5m, and 15m load averages from /proc/loadavg using a stack buffer.
func ReadLoadAverages(path string) (load1, load5, load15 float64) {
	var buf [128]byte
	n := readFirstBytes(path, buf[:])
	if n == 0 {
		return 0, 0, 0
	}
	parts := strings.Fields(string(buf[:n]))
	if len(parts) >= 3 {
		load1, _ = strconv.ParseFloat(parts[0], 64)
		load5, _ = strconv.ParseFloat(parts[1], 64)
		load15, _ = strconv.ParseFloat(parts[2], 64)
	}
	return round2(load1), round2(load5), round2(load15)
}

// ReadHostMemoryStats parses MemTotal and MemAvailable from the first 256 bytes of /proc/meminfo.
func ReadHostMemoryStats(path string) (usedMB, totalMB, usagePct float64) {
	var buf [256]byte
	n := readFirstBytes(path, buf[:])
	if n == 0 {
		return 0, 0, 0
	}

	var totalKB, availKB float64
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseProcKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			availKB = parseProcKB(line)
		}
		if totalKB > 0 && availKB > 0 {
			break
		}
	}

	if totalKB <= 0 {
		return 0, 0, 0
	}
	totalMB = totalKB / 1024.0
	usedMB = (totalKB - availKB) / 1024.0
	if usedMB < 0 {
		usedMB = 0
	}
	usagePct = (usedMB / totalMB) * 100.0
	return round2(usedMB), round2(totalMB), round2(usagePct)
}

func parseProcKB(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		val, _ := strconv.ParseFloat(fields[1], 64)
		return val
	}
	return 0
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

