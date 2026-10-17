package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/pin"
)

// initParams 是一次 init 需要的全部输入,CLI 从 flag 和提示里组出来,向导从表单组出来。
type initParams struct {
	Host, SSHUser                                       string
	SSHPort                                             int
	Password                                            string // SSH 密码;公钥装好后由 runInitSteps 置空
	Identity, Binary, InstallScript, Addr               string
	APIPort                                             int
	Name, Username, SNI, RealityDest, Remark, SubPrefix string
	DevicePassword                                      string
	// FromUI:由窗口里的首次安装向导发起。报错与收尾提示改说界面操作,不提命令
	// (窗口里显示的就是这些文字)。
	FromUI bool
}

// initProgress 接收进度。CLI 实现原样写终端;向导实现把它记进任务。
type initProgress interface {
	Step(format string, a ...any)
	Note(format string, a ...any)
	Stdout() io.Writer // 远端命令输出与收尾提示
	Stderr() io.Writer
}

// cliProgress 是 initProgress 的 CLI 实现:进度走 stderr,收尾与远端输出走 stdout。
type cliProgress struct{}

func (cliProgress) Step(format string, a ...any) { step(format, a...) }
func (cliProgress) Note(format string, a ...any) { note(format, a...) }
func (cliProgress) Stdout() io.Writer            { return os.Stdout }
func (cliProgress) Stderr() io.Writer            { return os.Stderr }

// initResult 是一次 init 的产出:接入地址、控制面地址、指纹,以及本机是否已接入。
type initResult struct {
	HostPort, Server, Fingerprint string
	Connected                     bool // 本机已接入
}

// runInitSteps 是 init 的主干:从连上 VPS 到把本机接进去的全部步骤,进度交给 prog。
// CLI 和向导共用这一条路径,差别只在 initParams 和 initProgress。
func runInitSteps(ctx context.Context, p *initParams, prog initProgress) (*initResult, error) {
	host := p.Host
	n := model.Node{
		Name:    "init",
		Address: host,
		SSHPort: p.SSHPort,
		SSHUser: p.SSHUser,
	}

	auth, err := initAuth(ctx, n, p, prog)
	if err != nil {
		return nil, err
	}

	// ---- 看清对面是什么 -------------------------------------------------------

	prog.Step("看清这台机器")
	probeCtx, cancel := context.WithTimeout(ctx, initProbeTimeout)
	defer cancel()
	info, err := probeRemote(probeCtx, n, auth)
	if err != nil {
		return nil, err
	}
	if info["os"] != "Linux" {
		return nil, fmt.Errorf("控制面要在 Linux 上跑,这台机器报的是 %s", info["os"])
	}
	if info["uid"] != "0" {
		return nil, fmt.Errorf("要用 root 登录(%s 现在的 uid 是 %s):装 systemd 服务、写 /etc/mpx 都要 root",
			n.SSHUserOrDefault(), info["uid"])
	}
	prog.Note("%s %s,root,fetch=%s", info["os"], info["arch"], firstNonEmpty(info["fetch"], "无"))

	// ---- 把安装脚本送上去 -----------------------------------------------------

	prog.Step("把安装脚本送上去")
	if err := uploadInstallScript(ctx, n, auth, info, p, prog); err != nil {
		return nil, err
	}

	// ---- 需要的话,把本地编好的二进制也送上去 ---------------------------------

	binArg := ""
	if bp := strings.TrimSpace(p.Binary); bp != "" {
		prog.Step("把本机的二进制送上去(可能几分钟,别掐)")

		content, err := os.ReadFile(bp)
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", bp, err)
		}
		upCtx, cancel := context.WithTimeout(ctx, initUploadTimeout)
		defer cancel()
		if err := initSSH.PutFile(upCtx, n, auth, initRemoteBinary, content); err != nil {
			return nil, err
		}
		prog.Note("已送达 %s(%s)", initRemoteBinary, humanBytes(int64(len(content))))
		binArg = initRemoteBinary
	}

	// ---- 装控制面 -------------------------------------------------------------

	prog.Step("在 VPS 上装控制面(监听 %s)", p.Addr)

	install := fmt.Sprintf("bash %s --addr %s", node.ShellQuote(initRemoteScript), node.ShellQuote(p.Addr))
	if binArg != "" {
		install += " " + node.ShellQuote(binArg)
	}
	installCtx, cancel := context.WithTimeout(ctx, initInstallTimeout)
	defer cancel()
	if err := initSSH.RunIO(installCtx, n, auth, install, prog.Stdout(), prog.Stderr()); err != nil {
		return nil, err
	}

	// 装到的 mpx 认不认识 bootstrap 子命令 —— 不认的话后面必然失败,不如现在就说
	// 清楚。发布件比这个功能旧的时候就是这样,错误信息得能直接指出该做什么。
	checkCtx, cancel := context.WithTimeout(ctx, initProbeTimeout)
	defer cancel()
	out, err := initSSH.Run(checkCtx, n, auth,
		"mpx bootstrap --help >/dev/null 2>&1 && echo MPX_BOOTSTRAP_OK || echo MPX_BOOTSTRAP_MISSING")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(out, "MPX_BOOTSTRAP_OK") {
		return nil, errors.New("装上的 mpx 还没有 bootstrap 子命令(多半是发布件比这个功能旧)。" +
			"用 --binary 指一个本机编好的 linux mpx,或等新版发布后再试")
	}

	// ---- 让控制面自己把对象建出来 ---------------------------------------------

	prog.Step("建节点、入站、入口、订阅设置与用户")
	bootArgs := []string{
		"mpx", "bootstrap",
		"--address", host,
		"--ssh-user", n.SSHUserOrDefault(),
		"--ssh-port", strconv.Itoa(n.SSHPortOrDefault()),
		"--api-port", strconv.Itoa(p.APIPort),
		"--name", p.Name,
		"--username", p.Username,
		"--sni", p.SNI,
		"--reality-dest", p.RealityDest,
	}
	if p.Remark != "" {
		bootArgs = append(bootArgs, "--remark", p.Remark)
	}
	if p.SubPrefix != "" {
		bootArgs = append(bootArgs, "--sub-prefix", p.SubPrefix)
	}

	bootCtx, cancel := context.WithTimeout(ctx, initBootstrapTimeout)
	defer cancel()
	if err := initSSH.RunIO(bootCtx, n, auth, shellJoin(bootArgs), prog.Stdout(), prog.Stderr()); err != nil {
		return nil, err
	}

	// ---- 收尾:设备密码 + 把本机接上去 --------------------------------------

	// 指纹是经 SSH 读回来的:SSH 这条路本身已经认过这台机器,所以这里不用再让人
	// 对一遍指纹。
	fpCtx, cancel := context.WithTimeout(ctx, initProbeTimeout)
	defer cancel()
	fp := ""
	if out, err := initSSH.Run(fpCtx, n, auth, "cat /var/lib/mpx/tls-fingerprint"); err == nil {
		fp, _ = pin.Normalize(strings.TrimSpace(out))
	}
	hostport := net.JoinHostPort(host, strconv.Itoa(p.APIPort))
	server := "https://" + hostport

	fmt.Fprintln(prog.Stdout())
	switch {
	case fp == "":
		fmt.Fprintln(prog.Stdout(), "没读到控制面的证书指纹(/var/lib/mpx/tls-fingerprint),本机没有自动接入。")
		if p.FromUI {
			fmt.Fprintf(prog.Stdout(), "稍后在 MPX 里选「接入已有控制面」,地址 %s(会显示指纹让你确认)\n", hostport)
		} else {
			fmt.Fprintf(prog.Stdout(), "稍后在本机跑:mpx connect %s(它会显示指纹让你确认)\n", hostport)
		}
		return &initResult{HostPort: hostport, Server: server}, nil
	case p.DevicePassword == "":
		fmt.Fprintln(prog.Stdout(), "没设设备密码,本机没有自动接入。")
		return &initResult{HostPort: hostport, Server: server}, nil
	}

	prog.Step("设置设备密码")
	// 在 VPS 本机经回环调接口:密码只走 SSH,不经公网。
	setPW := fmt.Sprintf(`MPX_TOKEN="$(sed -n 's/^MPX_TOKEN=//p' /etc/mpx/env)" mpx --server http://127.0.0.1:%d device password --password-stdin`, p.APIPort)
	pwCtx, cancel := context.WithTimeout(ctx, initProbeTimeout)
	defer cancel()
	if out, err := initSSH.RunWithInput(pwCtx, n, auth, setPW, []byte(p.DevicePassword+"\n")); err != nil {
		if p.FromUI {
			return nil, fmt.Errorf("设置设备密码失败(VPS 上装的 MPX 可能比这个功能旧):%v %s", err, strings.TrimSpace(out))
		}
		return nil, fmt.Errorf("设置设备密码失败(装上的 mpx 可能比这个功能旧,可用 --binary 指本机编好的 linux mpx):%v %s", err, strings.TrimSpace(out))
	}

	prog.Step("把这台电脑接入控制面")
	if _, err := initConnect(server, fp, p.DevicePassword, defaultDeviceName()); err != nil {
		if p.FromUI {
			return nil, fmt.Errorf("本机接入失败: %w;控制面已经装好,稍后在 MPX 里选「接入已有控制面」,地址 %s,核对指纹 %s", uiError(err), hostport, fp)
		}
		return nil, fmt.Errorf("本机接入失败: %w;稍后可手动 mpx connect %s --fp %s", err, hostport, fp)
	}

	fmt.Fprintln(prog.Stdout())
	if p.FromUI {
		fmt.Fprintf(prog.Stdout(), "别的设备接入这个控制面:在 MPX 里选「接入已有控制面」,地址 %s,核对指纹 %s,再输入刚设的设备密码。\n", hostport, fp)
	} else {
		fmt.Fprintln(prog.Stdout(), "别的设备接入这个控制面,装好 mpx 后跑这一行,再输入刚设的设备密码:")
		fmt.Fprintf(prog.Stdout(), "  mpx connect %s --fp %s\n", hostport, fp)
	}
	fmt.Fprintln(prog.Stdout())
	fmt.Fprintln(prog.Stdout(), "同一个端口上,管理接口走 HTTPS(控制面自签证书,按上面的指纹认),订阅走 HTTP。")
	return &initResult{HostPort: hostport, Server: server, Fingerprint: fp, Connected: true}, nil
}
