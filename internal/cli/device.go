package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// device 管"哪些设备接入了这个控制面"。每台设备一枚自己的 token,丢了一台只吊销
// 那一台;管理 token 是主钥匙,不在这里出现。
var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "查看与吊销接入控制面的设备",
}

var deviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出接入的设备(* 标出当前这台)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ds, raw, err := fetchDevices()
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		rows := make([][]string, 0, len(ds))
		for _, d := range ds {
			rows = append(rows, deviceRow(d, time.Now()))
		}
		return writeTable([]string{"", "ID", "名称", "接入时间", "最近使用", "最近 IP"}, rows)
	},
}

var deviceRevokeCmd = &cobra.Command{
	Use:   "revoke <id|名称>",
	Short: "吊销一台设备:它的 token 立即失效,要再用得重新 mpx connect",
	Example: `  mpx device revoke 3
  mpx device revoke old-laptop`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ds, _, err := fetchDevices()
		if err != nil {
			return err
		}
		d, err := pickDevice(ds, args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		if _, err := c.do("DELETE", "/api/devices/"+strconv.FormatInt(d.ID, 10), nil); err != nil {
			return err
		}
		if d.Current {
			fmt.Printf("已吊销设备 %d(%s)—— 就是这台,之后的命令要重新 mpx connect\n", d.ID, d.Name)
			return nil
		}
		fmt.Printf("已吊销设备 %d(%s)\n", d.ID, d.Name)
		return nil
	},
}

func fetchDevices() ([]model.Device, []byte, error) {
	c, err := newClient()
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.do("GET", "/api/devices", nil)
	if err != nil {
		return nil, nil, err
	}
	ds, err := decode[[]model.Device](raw)
	return ds, raw, err
}

// pickDevice 按 id 或名称找设备。名称可能重名(两台都叫 MacBook),重名时要求用 id。
func pickDevice(ds []model.Device, arg string) (model.Device, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		for _, d := range ds {
			if d.ID == id {
				return d, nil
			}
		}
	}
	var hit []model.Device
	for _, d := range ds {
		if d.Name == arg {
			hit = append(hit, d)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return model.Device{}, fmt.Errorf("没有 id 或名称为 %q 的设备;mpx device list 看看", arg)
	default:
		return model.Device{}, fmt.Errorf("有 %d 台设备都叫 %q,用 id 指定", len(hit), arg)
	}
}

func deviceRow(d model.Device, now time.Time) []string {
	mark := ""
	if d.Current {
		mark = "*"
	}
	used := "-"
	if d.LastUsedAt != nil {
		used = humanDuration(now.Sub(*d.LastUsedAt)) + "前"
	}
	return []string{mark, strconv.FormatInt(d.ID, 10), d.Name,
		d.CreatedAt.Format("2006-01-02 15:04"), used, dash(d.LastIP)}
}

func init() {
	deviceCmd.AddCommand(deviceListCmd, deviceRevokeCmd)
	rootCmd.AddCommand(deviceCmd)
}
