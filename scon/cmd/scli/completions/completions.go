package completions

import (
	"encoding/json"
	"os"
	"os/user"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/spf13/cobra"
)

func Machines(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	containers, err := scli.Client().ListContainers()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(containers))

	// running first
	for _, c := range containers {
		// remove dupes (can't specify same machine twice)
		if c.Record.State == types.ContainerStateRunning && !slices.Contains(args, c.Record.Name) {
			completions = append(completions, cobra.CompletionWithDesc(c.Record.Name, "running"))
		}
	}

	// then stopped
	for _, c := range containers {
		if c.Record.State == types.ContainerStateStopped && !slices.Contains(args, c.Record.Name) {
			completions = append(completions, c.Record.Name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

func MachinesOrAll(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if slices.Contains(args, "-a") || slices.Contains(args, "--all") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return Machines(cmd, args, toComplete)
}

func dockerClient() (*dockerclient.Client, error) {
	return dockerclient.NewWithUnixSocket(conf.DockerSocket(), nil)
}

func DockerVolumes(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	dockerClient, err := dockerClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	volumes, err := dockerClient.ListVolumes()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(volumes))
	for _, v := range volumes {
		completions = append(completions, cobra.CompletionWithDesc(v.Name, "volume"))
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func Limit(limit int, fn cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		// crude heuristic: skip 1 arg after every arg that starts with "-"
		// update: cobra doesn't seem to pass us flags, so no need
		/*
			dashArgs := 0
			for _, arg := range args {
				if strings.HasPrefix(arg, "-") {
					dashArgs++
				}
			}
		*/

		if len(args) >= limit {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return fn(cmd, args, toComplete)
	}
}

func LocalUsername(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	u, err := user.Current()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return []cobra.Completion{
		cobra.CompletionWithDesc(u.Username, "user"),
		cobra.CompletionWithDesc("root", "user"),
	}, cobra.ShellCompDirectiveNoFileComp
}

func TwoArgs(fn1 cobra.CompletionFunc, fn2 cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return fn1(cmd, args, toComplete)
		} else if len(args) == 1 {
			return fn2(cmd, args, toComplete)
		} else {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

func FileExtensionTarZst(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{"zst", "tar.zst"}, cobra.ShellCompDirectiveFilterFileExt
}

func RemoteUsername(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// TODO
	return LocalUsername(cmd, args, toComplete)
}

func RemoteDirectorySSH(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// TODO
	return nil, cobra.ShellCompDirectiveDefault
}

func RemoteDirectoryDocker(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// TODO
	return nil, cobra.ShellCompDirectiveDefault
}

func RemoteShellCommand(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// TODO
	return nil, cobra.ShellCompDirectiveDefault
}

func DockerContext(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	contextsDir := conf.UserDockerDir() + "/contexts/meta"
	files, err := os.ReadDir(contextsDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(files))
	for _, subdir := range files {
		jsonStr, err := os.ReadFile(contextsDir + "/" + subdir.Name() + "/meta.json")
		if err != nil {
			continue
		}

		var meta dockerclient.ContextMetadata
		err = json.Unmarshal(jsonStr, &meta)
		if err != nil {
			continue
		}

		host := meta.Endpoints.Docker.Host
		if host == "" {
			host = "?"
		}
		completions = append(completions, cobra.CompletionWithDesc(meta.Name, host))
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func MachineImage(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	distros := images.Distros()

	// dedupe
	completions := make(map[cobra.Completion]struct{})
	for _, distro := range distros {
		completions[cobra.CompletionWithDesc(distro, "latest")] = struct{}{}
		if oldestVersion, ok := images.ImageToOldestVersion[distro]; ok {
			completions[cobra.CompletionWithDesc(distro+":"+oldestVersion, "old")] = struct{}{}
		}
		if latestVersion, ok := images.ImageToLatestVersion[distro]; ok {
			completions[cobra.CompletionWithDesc(distro+":"+latestVersion, "latest")] = struct{}{}
		}

		for version, codename := range images.ImageVersionAliases {
			if version.Image == distro {
				completions[cobra.CompletionWithDesc(distro+":"+version.Version, "version")] = struct{}{}
				completions[cobra.CompletionWithDesc(distro+":"+codename, "codename")] = struct{}{}
			}
		}
	}

	keys := make([]string, 0, len(completions))
	for k := range completions {
		keys = append(keys, k)
	}

	return keys, cobra.ShellCompDirectiveNoFileComp
}

func DockerContainers(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	dockerClient, err := dockerClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	containers, err := dockerClient.ListContainers(true)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(containers))

	// running containers go first
	for _, c := range containers {
		if c.State == "running" {
			for _, name := range c.Names {
				completions = append(completions, cobra.CompletionWithDesc(strings.TrimPrefix(name, "/"), "running"))
			}
		}
	}

	// then stopped containers
	for _, c := range containers {
		if c.State != "running" {
			for _, name := range c.Names {
				completions = append(completions, cobra.CompletionWithDesc(strings.TrimPrefix(name, "/"), "container"))
			}
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

func DockerImages(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	dockerClient, err := dockerClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	images, err := dockerClient.ListImages()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(images))
	for _, i := range images {
		// only tagged images
		for _, tag := range i.RepoTags {
			completions = append(completions, cobra.CompletionWithDesc(tag, "image"))
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func ConfigKeys(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	configMap, err := cliutil.GetSyntheticVmConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	completions := make([]cobra.Completion, 0, len(configMap))
	for key := range configMap {
		completions = append(completions, cobra.CompletionWithDesc(key, "config"))
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func ConfigValues(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	configMap, err := cliutil.GetSyntheticVmConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	key := args[0]
	if strings.HasSuffix(key, ".username") {
		return RemoteUsername(cmd, args, toComplete)
	}

	if value, ok := configMap[key]; ok {
		if _, ok := value.(bool); ok {
			return []cobra.Completion{cobra.CompletionWithDesc("true", "bool"), cobra.CompletionWithDesc("false", "bool")}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	return nil, cobra.ShellCompDirectiveDefault
}
