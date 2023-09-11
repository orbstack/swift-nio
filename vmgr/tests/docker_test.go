package tests

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
)

// verify overlay2, btrfs, cgroups, etc.
var expectDockerSystemInfo = `
{
  "Driver": "overlay2",
  "DriverStatus": [
    [
      "Backing Filesystem",
      "btrfs"
    ],
    [
      "Supports d_type",
      "true"
    ],
    [
      "Using metacopy",
      "false"
    ],
    [
      "Native Overlay Diff",
      "true"
    ],
    [
      "userxattr",
      "false"
    ]
  ],
  "Plugins": {
    "Volume": [
      "local"
    ],
    "Network": [
      "bridge",
      "host",
      "ipvlan",
      "macvlan",
      "null",
      "overlay"
    ],
    "Authorization": null,
    "Log": [
      "awslogs",
      "fluentd",
      "gcplogs",
      "gelf",
      "journald",
      "json-file",
      "local",
      "logentries",
      "splunk",
      "syslog"
    ]
  },
  "MemoryLimit": true,
  "SwapLimit": true,
  "CpuCfsPeriod": true,
  "CpuCfsQuota": true,
  "CPUShares": true,
  "CPUSet": true,
  "PidsLimit": true,
  "IPv4Forwarding": true,
  "BridgeNfIptables": true,
  "BridgeNfIp6tables": true,
  "Debug": false,
  "OomKillDisable": false,
  "LoggingDriver": "json-file",
  "CgroupDriver": "cgroupfs",
  "CgroupVersion": "2",
  "NEventsListener": 1,
  "OperatingSystem": "OrbStack",
  "OSVersion": "",
  "OSType": "linux",
  "Architecture": "aarch64",
  "IndexServerAddress": "https://index.docker.io/v1/",
  "RegistryConfig": {
    "AllowNondistributableArtifactsCIDRs": null,
    "AllowNondistributableArtifactsHostnames": null,
    "InsecureRegistryCIDRs": [
      "127.0.0.0/8"
    ],
    "IndexConfigs": {
      "docker.io": {
        "Name": "docker.io",
        "Mirrors": [],
        "Secure": true,
        "Official": true
      }
    },
    "Mirrors": null
  },
  "GenericResources": null,
  "DockerRootDir": "/var/lib/docker",
  "HttpProxy": "",
  "HttpsProxy": "",
  "NoProxy": "",
  "Name": "orbstack",
  "Labels": [],
  "ExperimentalBuild": false,
  "Runtimes": {
    "io.containerd.runc.v2": {
      "path": "runc"
    },
    "runc": {
      "path": "runc"
    }
  },
  "DefaultRuntime": "runc",
  "Swarm": {
    "NodeID": "",
    "NodeAddr": "",
    "LocalNodeState": "inactive",
    "ControlAvailable": false,
    "Error": "",
    "RemoteManagers": null
  },
  "LiveRestoreEnabled": false,
  "Isolation": "",
  "InitBinary": "docker-init",
  "InitCommit": {
    "ID": "de40ad0",
    "Expected": "de40ad0"
  },
  "SecurityOptions": [
    "name=seccomp,profile=builtin",
    "name=cgroupns"
  ],
  "ProductLicense": "Community Engine",
  "DefaultAddressPools": [
    {
      "Base": "192.168.215.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.228.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.247.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.207.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.167.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.107.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.237.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.148.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.214.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.165.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.227.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.181.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.158.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.117.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.155.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.147.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.229.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.183.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.156.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.97.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.171.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.186.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.216.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.242.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.166.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.239.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.223.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.164.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.163.0/24",
      "Size": 24
    },
    {
      "Base": "192.168.172.0/24",
      "Size": 24
    },
    {
      "Base": "172.17.0.0/16",
      "Size": 16
    },
    {
      "Base": "172.18.0.0/16",
      "Size": 16
    },
    {
      "Base": "172.19.0.0/16",
      "Size": 16
    },
    {
      "Base": "172.20.0.0/14",
      "Size": 16
    },
    {
      "Base": "172.24.0.0/14",
      "Size": 16
    },
    {
      "Base": "172.28.0.0/14",
      "Size": 16
    }
  ],
  "Warnings": null
}
`

func dockerClient() *dockerclient.Client {
	client, err := dockerclient.NewWithUnixSocket(conf.DockerSocket(), nil)
	if err != nil {
		panic(err)
	}

	return client
}

func TestDockerSystemInfo(t *testing.T) {
	t.Parallel()

	var obj map[string]any
	err := dockerClient().Call("GET", "/info", nil, &obj)
	if err != nil {
		t.Fatal(err)
	}

	// parse expected
	var expect map[string]any
	err = json.Unmarshal([]byte(expectDockerSystemInfo), &expect)
	if err != nil {
		t.Fatal(err)
	}

	// TODO replace default-address-pools with netconf

	// remove any keys not in expected
	for k := range obj {
		if _, ok := expect[k]; !ok {
			delete(obj, k)
		}
	}

	// compare
	if !reflect.DeepEqual(obj, expect) {
		t.Fatalf("got: %+v\nwant: %+v", obj, expect)
	}
}

func TestDockerEngineVersion(t *testing.T) {
	t.Parallel()

	// match CLI
	expectedVersion := readExpectedBinVersion(t, "DOCKER")

	// ask server
	var obj map[string]any
	err := dockerClient().Call("GET", "/version", nil, &obj)
	if err != nil {
		t.Fatal(err)
	}

	// compare
	if obj["Version"] != expectedVersion {
		t.Fatalf("Docker CLI and engine version mismatch. got: %+v\nwant: %+v", obj["Version"], expectedVersion)
	}
}
