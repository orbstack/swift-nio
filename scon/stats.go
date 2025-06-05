package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/dirfs"
	"golang.org/x/sys/unix"
)

func readCgroupStats(cgPath string, netDirfs *dirfs.FS, netDevPath string) (*types.StatsEntry, error) {
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
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 3 {
			continue
		}

		_, rBytesStr, ok := strings.Cut(parts[1], "=")
		if !ok {
			continue
		}
		rBytes, err := strconv.ParseUint(rBytesStr, 10, 64)
		if err != nil {
			return nil, err
		}
		_, wBytesStr, ok := strings.Cut(parts[2], "=")
		if !ok {
			continue
		}
		wBytes, err := strconv.ParseUint(wBytesStr, 10, 64)
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

	if netDirfs != nil {
		netDev, err := netDirfs.ReadFile(netDevPath)
		if err != nil {
			return nil, err
		}
		for line := range strings.SplitSeq(string(netDev), "\n") {
			parts := strings.Fields(line)
			if len(parts) < 10 {
				continue
			}

			if parts[0] == "eth0:" {
				rxBytes, err := strconv.ParseUint(parts[1], 10, 64)
				if err != nil {
					return nil, err
				}
				txBytes, err := strconv.ParseUint(parts[9], 10, 64)
				if err != nil {
					return nil, err
				}
				stats.NetRxBytes = &rxBytes
				stats.NetTxBytes = &txBytes
				break
			}
		}
	}

	return stats, nil
}

func (m *ConManager) GetStats(req types.StatsRequest) (types.StatsResponse, error) {
	// must not be nil (for json return)
	statEntries := make([]*types.StatsEntry, 0, 16)

	// 1. machines
	err := m.ForEachContainer(func(c *Container) error {
		rt, err := c.RuntimeState()
		if err != nil {
			// stopped
			return nil
		}

		entry, err := readCgroupStats(rt.cgroupPath, rt.InitProcDirfd, "net/dev")
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

		return nil
	})
	if err != nil {
		return types.StatsResponse{
			Entries: statEntries,
		}, err
	}

	// 2. containers
	dockerMachine := m.sconGuest.dockerMachine
	dockerRt, err := dockerMachine.RuntimeState()
	if err != nil {
		dockerCgPath := dockerRt.cgroupPath + "/" + ChildCgroupName
		err = m.sconGuest.ForEachDockerContainer(func(ctr containerWithMeta) error {
			ctrCgPath := dockerRt.cgroupPath + "/" + ChildCgroupName + "/" + ctr.CgroupPath
			entry, err := readCgroupStats(ctrCgPath, dockerRt.InitProcDirfd, "root/proc/"+strconv.Itoa(ctr.Pid)+"/net/dev")
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

		// add a few special cgroups:
		// $DOCKER/init.scope = dockerd, containerd
		// $DOCKER/k3s = k3s services
		// $DOCKER/docker/buildkit = builds
		entry, err := readCgroupStats(dockerCgPath+"/init.scope", nil, "")
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				// ignore: race - stopped
			} else {
				return types.StatsResponse{
					Entries: statEntries,
				}, err
			}
		} else {
			entry.Entity = types.StatsEntity{
				Service: &types.StatsEntityService{
					ID: "dockerd",
				},
			}
			statEntries = append(statEntries, entry)
		}

		if m.k8sEnabled {
			entry, err := readCgroupStats(dockerCgPath+"/k3s", nil, "")
			if err != nil {
				if errors.Is(err, unix.ENOENT) {
					// ignore: race - stopped
				} else {
					return types.StatsResponse{
						Entries: statEntries,
					}, err
				}
			} else {
				entry.Entity = types.StatsEntity{
					Service: &types.StatsEntityService{
						ID: "k8s",
					},
				}
				statEntries = append(statEntries, entry)
			}
		}

		entry, err = readCgroupStats(dockerCgPath+"/docker/buildkit", nil, "")
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				// ignore: race - stopped
			} else {
				return types.StatsResponse{
					Entries: statEntries,
				}, err
			}
		} else {
			entry.Entity = types.StatsEntity{
				Service: &types.StatsEntityService{
					ID: "buildkit",
				},
			}
			statEntries = append(statEntries, entry)
		}
	}

	return types.StatsResponse{
		Entries: statEntries,
	}, nil
}
