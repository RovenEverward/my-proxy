# 在本机试用

完成本教程后，你会在自己电脑上运行一个控制面，建好一个用户，并在浏览器里看到他的订阅页。全程不需要服务器，大约 5 分钟。

## 准备

- 已安装 `mpx`（见[快速开始](/tutorial/quickstart)第 1 步）。
- 两个终端窗口。

## 第 1 步：启动控制面

在第一个终端运行：

```bash
mpx serve --token demo-token
```

控制面监听 `127.0.0.1:8080`，数据存在用户配置目录下的 `mpx/mpx.db`（Linux 是 `~/.config/mpx/`，macOS 是 `~/Library/Application Support/mpx/`）。这个窗口保持打开。

## 第 2 步：让命令行连上它

在第二个终端运行：

```console
$ mpx login --server http://127.0.0.1:8080 --token demo-token
已写入 /home/you/.config/mpx/config.json(当前配置档 default)
```

确认连上了：

```console
$ mpx status
字段       值
版本       v0.3.3-0.20261010190022-a7ac1ebdd573
已运行      2 秒
后台周期     30 秒
节点       0
用户       0
...
```

## 第 3 步：建一个用户

```console
$ mpx user add alice --limit 100GB --reset-strategy monthly --expire 2026-12-31 --note 演示
已创建用户 alice
```

## 第 4 步：拿到订阅地址

```console
$ mpx user links alice
格式            订阅地址
auto          http://127.0.0.1:8080/sub/F5qwEAdMULZZRk7Y
links         http://127.0.0.1:8080/sub/F5qwEAdMULZZRk7Y/links
clash-meta    http://127.0.0.1:8080/sub/F5qwEAdMULZZRk7Y/clash-meta
sing-box      http://127.0.0.1:8080/sub/F5qwEAdMULZZRk7Y/sing-box
...
```

你的 token 段和这里不同。

## 第 5 步：在浏览器里打开

把 `auto` 那一行的地址粘到浏览器。你会看到 alice 的订阅页：状态、用量上限 100GB、到期日 2026-12-31。

现在还没有节点，所以订阅里没有可用线路。`mpx doctor` 会明确告诉你这一点：

```console
$ mpx doctor
节点               warn  还没有任何节点 → mpx node add 加一台,再加一个入站才会有订阅
```

## 你学到了什么

你运行了控制面，用 `login` 保存了连接，建了用户，并看到了一条地址如何同时服务客户端和浏览器。要接入真实节点，回到[快速开始](/tutorial/quickstart)。
