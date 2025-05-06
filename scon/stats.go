package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

func readCgroupStats(cgPath string) (*types.StatsEntry, error) {
	sysPath := "/sys/fs/cgroup/" + cgPath
	stats := &types.StatsEntry{
		ID: types.StatsID{
			CgroupPath: &types.StatsIDCgroup{
				Value: cgPath,
			},
		},
	}

	cpuStr, err := util.ReadFileFast(sysPath + "/cpu.stat")
	if err != nil {
		return nil, err
	}
	cpuStat := strings.Split(string(cpuStr), "\n")
	usageUsec := strings.Split(cpuStat[0], " ")
	if len(usageUsec) != 2 {
		return nil, fmt.Errorf("invalid cpu.stat")
	}
	usageUsecInt, err := strconv.ParseUint(usageUsec[1], 10, 64)
	if err != nil {
		return nil, err
	}
	stats.CPUUsageUsec = usageUsecInt

	ioStatStr, err := util.ReadFileFast(sysPath + "/io.stat")
	if err != nil {
		return nil, err
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
			return nil, err
		}
		wBytes, err := strconv.ParseUint(strings.Split(parts[2], "=")[1], 10, 64)
		if err != nil {
			return nil, err
		}
		readBytes += rBytes
		writeBytes += wBytes
	}
	stats.DiskReadBytes = readBytes
	stats.DiskWriteBytes = writeBytes

	memoryStr, err := util.ReadFileFast(sysPath + "/memory.current")
	if err != nil {
		return nil, err
	}
	memoryCurrent, err := strconv.ParseUint(strings.TrimSpace(string(memoryStr)), 10, 64)
	if err != nil {
		return nil, err
	}
	stats.MemoryBytes = memoryCurrent

	pidsStr, err := util.ReadFileFast(sysPath + "/pids.current")
	if err != nil {
		return nil, err
	}
	pidsCurrent, err := strconv.ParseUint(strings.TrimSpace(string(pidsStr)), 10, 32)
	if err != nil {
		return nil, err
	}
	stats.NumProcesses = uint32(pidsCurrent)

	return stats, nil
}

func (m *ConManager) GetStats(req types.StatsRequest) (types.StatsResponse, error) {
	// must not be nil (for json return)
	statEntries := make([]*types.StatsEntry, 0, 16)

	// 1. machines
	dockerMachine := m.sconGuest.dockerMachine
	err := m.ForEachContainer(func(c *Container) error {
		cgPath := c.lastCgroupPath
		if c.Running() && cgPath != "" {
			entry, err := readCgroupStats(cgPath)
			if err != nil {
				if errors.Is(err, unix.ENOENT) {
					// ignore: race - stopped
				} else {
					return err
				}
			} else {
				entry.Entity = types.StatsEntity{
					Machine: &types.StatsEntityMachine{
						ID: c.ID,
					},
				}

				statEntries = append(statEntries, entry)
			}
		}

		return nil
	})
	if err != nil {
		return types.StatsResponse{
			Entries: statEntries,
		}, err
	}

	// 2. containers
	dockerCgBase := dockerMachine.lastCgroupPath
	if dockerCgBase != "" {
		dockerCgBase += "/" + ChildCgroupName
		err = m.sconGuest.ForEachDockerContainer(func(ctr containerWithMeta) error {
			ctrCgPath := dockerCgBase + "/" + ctr.CgroupPath
			entry, err := readCgroupStats(ctrCgPath)
			if err != nil {
				if errors.Is(err, unix.ENOENT) {
					// ignore: race - stopped
				} else {
					return err
				}
			} else {
				entry.Entity = types.StatsEntity{
					Container: &types.StatsEntityContainer{
						ID: ctr.ID,
					},
				}

				statEntries = append(statEntries, entry)
			}

			return nil
		})
		if err != nil {
			return types.StatsResponse{
				Entries: statEntries,
			}, err
		}
	}

	return types.StatsResponse{
		Entries: statEntries,
	}, nil
}
