package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
)

func readCgroupStats(cgPath string) (types.StatsEntry, error) {
	sysPath := "/sys/fs/cgroup/" + cgPath
	stats := types.StatsEntry{
		ID: types.StatsID{
			CgroupPath: &types.StatsIDCgroup{
				Value: cgPath,
			},
		},
	}

	cpuStr, err := util.ReadFileFast(sysPath + "/cpu.stat")
	if err != nil {
		return stats, err
	}
	cpuStat := strings.Split(string(cpuStr), "\n")
	usageUsec := strings.Split(cpuStat[0], " ")
	if len(usageUsec) != 2 {
		return stats, fmt.Errorf("invalid cpu.stat")
	}
	usageUsecInt, err := strconv.ParseUint(usageUsec[1], 10, 64)
	if err != nil {
		return stats, err
	}
	stats.CPUUsageUsec = usageUsecInt

	ioStatStr, err := util.ReadFileFast(sysPath + "/io.stat")
	if err != nil {
		return stats, err
	}
	ioStat := strings.Split(string(ioStatStr), "\n")
	var readBytes, writeBytes uint64
	for _, line := range ioStat {
		parts := strings.Split(line, " ")
		if len(parts) < 3 {
			continue
		}

		rBytes, err := strconv.ParseUint(strings.Split(parts[1], "=")[1], 10, 64)
		if err != nil {
			return stats, err
		}
		wBytes, err := strconv.ParseUint(strings.Split(parts[2], "=")[1], 10, 64)
		if err != nil {
			return stats, err
		}
		readBytes += rBytes
		writeBytes += wBytes
	}
	stats.DiskReadBytes = readBytes
	stats.DiskWriteBytes = writeBytes

	memoryStr, err := util.ReadFileFast(sysPath + "/memory.current")
	if err != nil {
		return stats, err
	}
	memoryCurrent, err := strconv.ParseUint(strings.TrimSpace(string(memoryStr)), 10, 64)
	if err != nil {
		return stats, err
	}
	stats.MemoryBytes = memoryCurrent

	pidsStr, err := util.ReadFileFast(sysPath + "/pids.current")
	if err != nil {
		return stats, err
	}
	pidsCurrent, err := strconv.ParseUint(strings.TrimSpace(string(pidsStr)), 10, 32)
	if err != nil {
		return stats, err
	}
	stats.NumProcesses = uint32(pidsCurrent)

	return stats, nil
}

func (m *ConManager) GetStats(req types.StatsRequest) (types.StatsResponse, error) {
	var statEntries []types.StatsEntry
	err := m.ForEachContainer(func(c *Container) error {
		cgPath := c.lastCgroupPath
		if c.Running() && cgPath != "" {
			entry, err := readCgroupStats(cgPath)
			if err != nil {
				return err
			}
			statEntries = append(statEntries, entry)
		}
		return nil
	})

	return types.StatsResponse{
		Entries: statEntries,
	}, err
}
