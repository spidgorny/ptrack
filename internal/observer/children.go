package observer

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ptrack/internal/model"
)

type Snapshot struct {
	RootCPU  float64
	Children []model.ChildProcess
}

type Snapshotter interface {
	Snapshot(parentPID int, previous []model.ChildProcess, now time.Time) (Snapshot, error)
}

type NopSnapshotter struct{}

func (NopSnapshotter) Snapshot(parentPID int, previous []model.ChildProcess, now time.Time) (Snapshot, error) {
	return Snapshot{Children: []model.ChildProcess{}}, nil
}

type PSSnapshotter struct{}

type psRow struct {
	pid        int
	ppid       int
	cpuPercent float64
	status     string
	command    string
	argv       []string
}

func (PSSnapshotter) Snapshot(parentPID int, previous []model.ChildProcess, now time.Time) (Snapshot, error) {
	if parentPID <= 0 {
		return Snapshot{Children: []model.ChildProcess{}}, nil
	}

	out, err := exec.Command("ps", "-axo", "pid=,ppid=,stat=,%cpu=,command=").Output()
	if err != nil {
		return Snapshot{}, err
	}

	rows := parsePSRows(string(out))
	root, ok := rows[parentPID]
	if !ok {
		return Snapshot{Children: []model.ChildProcess{}}, nil
	}

	return Snapshot{
		RootCPU:  root.cpuPercent,
		Children: descendants(rows, parentPID, previous, now),
	}, nil
}

func parsePSRows(raw string) map[int]psRow {
	rows := make(map[int]psRow)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cpuPercent, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			cpuPercent = 0
		}

		argv := append([]string(nil), fields[4:]...)
		command := ""
		if len(argv) > 0 {
			command = filepath.Base(argv[0])
		}
		status := "running"
		if strings.HasPrefix(fields[2], "Z") {
			status = "exited"
		}

		rows[pid] = psRow{
			pid:        pid,
			ppid:       ppid,
			cpuPercent: cpuPercent,
			status:     status,
			command:    command,
			argv:       argv,
		}
	}
	return rows
}

func descendants(rows map[int]psRow, parentPID int, previous []model.ChildProcess, now time.Time) []model.ChildProcess {
	byParent := make(map[int][]psRow)
	for _, row := range rows {
		byParent[row.ppid] = append(byParent[row.ppid], row)
	}

	previousByPID := make(map[int]model.ChildProcess, len(previous))
	for _, child := range previous {
		previousByPID[child.PID] = child
	}

	queue := []int{parentPID}
	items := make([]model.ChildProcess, 0, len(rows))
	seen := make(map[int]struct{})
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, row := range byParent[pid] {
			if _, ok := seen[row.pid]; ok {
				continue
			}
			seen[row.pid] = struct{}{}
			queue = append(queue, row.pid)

			startedAt := now.UTC()
			if previousChild, ok := previousByPID[row.pid]; ok && !previousChild.StartedAt.IsZero() {
				startedAt = previousChild.StartedAt
			}

			items = append(items, model.ChildProcess{
				PID:        row.pid,
				PPID:       row.ppid,
				Command:    row.command,
				Argv:       append([]string(nil), row.argv...),
				Status:     row.status,
				StartedAt:  startedAt,
				CPUPercent: row.cpuPercent,
			})
		}
	}

	for pid, previousChild := range previousByPID {
		if _, ok := seen[pid]; ok {
			continue
		}
		retained := previousChild
		retained.Status = "exited"
		retained.CPUPercent = 0
		if retained.FinishedAt == nil {
			finishedAt := now.UTC()
			retained.FinishedAt = &finishedAt
		}
		retained.Argv = append([]string(nil), previousChild.Argv...)
		items = append(items, retained)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].PID < items[j].PID })
	return items
}
