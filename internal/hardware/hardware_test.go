package hardware

import (
	"testing"
	"time"
)

// These read files the kernel maintains, and a mis-read shows up as a wrong
// number on the overview page — which is worse than no number, because
// somebody acts on it.
func TestTheProcessorUsageIsADifferenceBetweenTwoReadings(t *testing.T) {
	// The counters in /proc/stat only go up, so a single reading says
	// nothing. The first call must not invent a figure.
	collector := NewCollector("/")
	first := collector.Collect()
	if first.CPU.UsagePercent != 0 {
		t.Errorf("the first reading reported %.1f%%; it has nothing to compare against",
			first.CPU.UsagePercent)
	}

	// Give it something to measure.
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
	}

	second := collector.Collect()
	if second.CPU.UsagePercent < 0 || second.CPU.UsagePercent > 100 {
		t.Errorf("the second reading is %.1f%%, which is not a percentage", second.CPU.UsagePercent)
	}
}

func TestTheMachineReportsItselfPlausibly(t *testing.T) {
	snapshot := NewCollector("/").Collect()

	if snapshot.CPU.Count < 1 {
		t.Error("this machine reports no processors")
	}
	if snapshot.Memory.Total == 0 {
		t.Error("this machine reports no memory at all")
	}
	if snapshot.Memory.Used > snapshot.Memory.Total {
		t.Errorf("more memory is used (%d) than exists (%d)", snapshot.Memory.Used, snapshot.Memory.Total)
	}
	if snapshot.Memory.Available > snapshot.Memory.Total {
		t.Errorf("more memory is available (%d) than exists (%d)", snapshot.Memory.Available, snapshot.Memory.Total)
	}
	if snapshot.Uptime <= 0 {
		t.Error("this machine reports that it has not been running")
	}
	if snapshot.Hostname == "" {
		t.Error("this machine reports no name")
	}
}

func TestEveryFilesystemAskedAboutIsReported(t *testing.T) {
	snapshot := NewCollector("/", "/tmp").Collect()
	if len(snapshot.Disks) == 0 {
		t.Fatal("no filesystems were reported")
	}
	for _, disk := range snapshot.Disks {
		if disk.Total == 0 {
			t.Errorf("%s has no size", disk.Path)
		}
		if disk.Used > disk.Total {
			t.Errorf("%s uses more than it has: %d of %d", disk.Path, disk.Used, disk.Total)
		}
		if disk.Available > disk.Total {
			t.Errorf("%s has more available than it has: %d of %d", disk.Path, disk.Available, disk.Total)
		}
	}
}

func TestAFilesystemThatIsNotThereIsSkippedRatherThanReportedAsEmpty(t *testing.T) {
	// A device configured with a state directory that has not been created
	// yet must not show a zero-sized disk on the overview.
	snapshot := NewCollector("/nonexistent/path").Collect()
	if len(snapshot.Disks) != 0 {
		t.Errorf("a filesystem that does not exist was reported: %v", snapshot.Disks)
	}
}

func TestTemperaturesAreInDegreesRatherThanMillidegrees(t *testing.T) {
	// The kernel reports millidegrees. A display in a sealed enclosure on a
	// sunny wall throttles, and "45000 °C" on the overview helps nobody.
	snapshot := NewCollector("/").Collect()
	for _, sensor := range snapshot.Thermal {
		if sensor.Celsius > 200 || sensor.Celsius < -50 {
			t.Errorf("%s reports %.0f °C, which is not a temperature", sensor.Name, sensor.Celsius)
		}
		if sensor.Name == "" {
			t.Error("a sensor has no name, so nobody can tell which one is hot")
		}
	}
}

func TestCollectingNamesTheRootFilesystemWhenNothingIsAsked(t *testing.T) {
	snapshot := NewCollector().Collect()
	if len(snapshot.Disks) != 1 || snapshot.Disks[0].Path != "/" {
		t.Errorf("with no filesystems named, %v were reported; want just /", snapshot.Disks)
	}
}
