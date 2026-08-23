// Package hardware reports what the machine is and how it is doing, by
// reading /proc and /sys directly.
//
// Nothing here shells out and nothing here needs a library. Every value comes
// from a file the kernel maintains, which is the only source that is available
// inside a container with no tools in it, and which is also the fastest.
package hardware

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Snapshot is everything the interface shows about the machine.
type Snapshot struct {
	Hostname string `json:"hostname"`

	// Uptime is how long the machine — not the daemon — has been running.
	Uptime time.Duration `json:"uptime"`

	CPU     CPU      `json:"cpu"`
	Memory  Memory   `json:"memory"`
	Disks   []Disk   `json:"disks"`
	Thermal []Sensor `json:"thermal"`

	// LoadAverage is the one, five and fifteen minute figures.
	LoadAverage [3]float64 `json:"loadAverage"`
}

// CPU is how busy the processor is.
type CPU struct {
	Count int `json:"count"`

	// UsagePercent is the share of time the processor spent doing anything at
	// all since the previous reading. It is meaningless on the first reading,
	// which is why the collector keeps the previous one.
	UsagePercent float64 `json:"usagePercent"`

	Model string `json:"model"`
}

// Memory is in bytes throughout, because a percentage alone cannot answer
// "will another browser tab fit".
type Memory struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
	Used      uint64 `json:"used"`
	SwapTotal uint64 `json:"swapTotal"`
	SwapUsed  uint64 `json:"swapUsed"`
}

// Disk is one filesystem the daemon cares about.
type Disk struct {
	Path      string `json:"path"`
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
	Used      uint64 `json:"used"`
}

// Sensor is one temperature reading, in degrees Celsius. A display in a sealed
// enclosure on a sunny wall throttles, and the first sign is here.
type Sensor struct {
	Name    string  `json:"name"`
	Celsius float64 `json:"celsius"`
}

// Collector reads the machine's state. It keeps the previous processor
// reading, because processor usage is a difference between two samples and
// cannot be answered from one.
type Collector struct {
	mutex sync.Mutex

	previousBusy  uint64
	previousTotal uint64

	// paths are the filesystems reported. They are the ones that fill up and
	// stop a display working: the browser profile and the whole root.
	paths []string
}

// NewCollector returns a collector reporting on the given filesystems.
func NewCollector(paths ...string) *Collector {
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	return &Collector{paths: paths}
}

// Collect reads everything. It never returns an error: a machine where one of
// these files is missing — a container with no thermal zones, say — should
// still report the rest rather than nothing.
func (self *Collector) Collect() Snapshot {
	snapshot := Snapshot{
		CPU: CPU{Count: runtime.NumCPU()},
	}
	snapshot.Hostname, _ = os.Hostname()
	snapshot.Uptime = readUptime()
	snapshot.LoadAverage = readLoadAverage()
	snapshot.CPU.Model = readProcessorModel()
	snapshot.CPU.UsagePercent = self.readProcessorUsage()
	snapshot.Memory = readMemory()
	snapshot.Thermal = readThermal()

	for _, path := range self.paths {
		if disk, err := readDisk(path); err == nil {
			snapshot.Disks = append(snapshot.Disks, disk)
		}
	}

	return snapshot
}

func readUptime() time.Duration {
	content, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func readLoadAverage() [3]float64 {
	var average [3]float64
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return average
	}
	fields := strings.Fields(string(content))
	for index := 0; index < 3 && index < len(fields); index++ {
		average[index], _ = strconv.ParseFloat(fields[index], 64)
	}
	return average
}

func readProcessorModel() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "model name", "Model":
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// readProcessorUsage returns the share of processor time spent doing anything
// since the previous call. The first line of /proc/stat is a set of counters
// that only go up, so the answer is the ratio of two differences.
func (self *Collector) readProcessorUsage() float64 {
	content, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(content), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}

	var total, idle uint64
	for index, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			continue
		}
		total += value
		// The fourth and fifth counters are idle and waiting for input or
		// output; everything else is the processor doing something.
		if index == 3 || index == 4 {
			idle += value
		}
	}
	busy := total - idle

	self.mutex.Lock()
	defer self.mutex.Unlock()

	previousBusy, previousTotal := self.previousBusy, self.previousTotal
	self.previousBusy, self.previousTotal = busy, total

	if previousTotal == 0 || total <= previousTotal {
		return 0
	}
	return float64(busy-previousBusy) / float64(total-previousTotal) * 100
}

func readMemory() Memory {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}
	}
	defer func() { _ = file.Close() }()

	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, rest, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[name] = kilobytes * 1024
	}

	memory := Memory{
		Total:     values["MemTotal"],
		Available: values["MemAvailable"],
		SwapTotal: values["SwapTotal"],
	}
	if memory.Total >= memory.Available {
		memory.Used = memory.Total - memory.Available
	}
	if free, found := values["SwapFree"]; found && memory.SwapTotal >= free {
		memory.SwapUsed = memory.SwapTotal - free
	}
	return memory
}

func readDisk(path string) (Disk, error) {
	var statistics unix.Statfs_t
	if err := unix.Statfs(path, &statistics); err != nil {
		return Disk{}, fmt.Errorf("hardware: cannot read the filesystem at %s: %w", path, err)
	}
	blockSize := uint64(statistics.Bsize)
	disk := Disk{
		Path:      path,
		Total:     statistics.Blocks * blockSize,
		Available: statistics.Bavail * blockSize,
	}
	// Used is what a person means by used, which is not total minus available:
	// the blocks reserved for root are neither.
	disk.Used = (statistics.Blocks - statistics.Bfree) * blockSize
	return disk, nil
}

// readThermal reads every thermal zone the kernel exposes. The names are of
// the form "x86_pkg_temp" or "cpu-thermal", which is more use than "zone 0".
func readThermal() []Sensor {
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return nil
	}

	sensors := make([]Sensor, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "thermal_zone") {
			continue
		}
		directory := "/sys/class/thermal/" + entry.Name()

		raw, err := os.ReadFile(directory + "/temp")
		if err != nil {
			continue
		}
		millidegrees, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			continue
		}

		name := entry.Name()
		if kind, err := os.ReadFile(directory + "/type"); err == nil {
			name = strings.TrimSpace(string(kind))
		}
		sensors = append(sensors, Sensor{Name: name, Celsius: float64(millidegrees) / 1000})
	}
	return sensors
}
