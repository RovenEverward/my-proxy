package cli

import (
	"errors"
	"strconv"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

var nodeCoreCmd = &cobra.Command{Use: "core", Short: "查看和修改节点上各核心的 API 端口、配置路径与服务名"}
var nodeCoreFlags struct {
	apiPort                 int
	configPath, serviceName string
}

func init() {
	nodeCoreCmd.AddCommand(nodeCoreShowCmd, nodeCoreEditCmd)
	nodeCoreEditCmd.Flags().IntVar(&nodeCoreFlags.apiPort, "api-port", 0, "新的核心 API 端口,0 表示默认值")
	nodeCoreEditCmd.Flags().StringVar(&nodeCoreFlags.configPath, "config-path", "", "新的核心配置绝对路径,空表示默认值")
	nodeCoreEditCmd.Flags().StringVar(&nodeCoreFlags.serviceName, "service-name", "", "新的 systemd 服务名,空表示默认值")
}

func corePath(args []string) (string, error) {
	id, err := resolveNodeID(args[0], false)
	if err != nil {
		return "", err
	}
	core := model.Core(args[1])
	if !core.Valid() {
		return "", errors.New("核心只能是 xray / sing-box")
	}
	return nodePath(id) + "/cores/" + string(core), nil
}

func printCore(raw []byte) error {
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	d, err := decode[model.CoreDeployment](raw)
	if err != nil {
		return err
	}
	at := "从未"
	if d.DeployedAt != nil {
		at = d.DeployedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return writeKV([][2]string{{"节点", strconv.FormatInt(d.NodeID, 10)}, {"核心", string(d.Core)}, {"API 端口", strconv.Itoa(d.APIPort)}, {"配置路径", d.ConfigPath}, {"服务名", d.ServiceName}, {"下发哈希", dash(d.DeployedHash)}, {"下发时间", at}})
}

var nodeCoreShowCmd = &cobra.Command{
	Use: "show <节点> <xray|sing-box>", Short: "查看核心的生效部署信息", Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := corePath(args)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", path, nil)
		if err != nil {
			return err
		}
		return printCore(raw)
	},
}

var nodeCoreEditCmd = &cobra.Command{
	Use: "edit <节点> <xray|sing-box>", Short: "修改给出的核心部署字段", Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := corePath(args)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", path, nil)
		if err != nil {
			return err
		}
		d, err := decode[model.CoreDeployment](raw)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("api-port") {
			d.APIPort = nodeCoreFlags.apiPort
		}
		if cmd.Flags().Changed("config-path") {
			d.ConfigPath = nodeCoreFlags.configPath
		}
		if cmd.Flags().Changed("service-name") {
			d.ServiceName = nodeCoreFlags.serviceName
		}
		raw, err = c.do("PUT", path, d)
		if err != nil {
			return err
		}
		return printCore(raw)
	},
}
