package cli

import (
	"context"
	"io"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

// initRemote 是 init 用到的全部远端操作;默认实现直接转调 internal/node,测试换成假的。
type initRemote interface {
	EnsureKey(keyPath string) (string, error)
	Bootstrap(ctx context.Context, n model.Node, keyPath, pubkey, password string) error
	Run(ctx context.Context, n model.Node, a node.Auth, script string) (string, error)
	RunIO(ctx context.Context, n model.Node, a node.Auth, script string, stdout, stderr io.Writer) error
	RunWithInput(ctx context.Context, n model.Node, a node.Auth, script string, stdin []byte) (string, error)
	PutFile(ctx context.Context, n model.Node, a node.Auth, p string, content []byte) error
}

// nodeRemote 是 initRemote 的默认实现,每个方法一行转调 internal/node。
type nodeRemote struct{}

func (nodeRemote) EnsureKey(keyPath string) (string, error) {
	return node.EnsureSSHKey(keyPath)
}

func (nodeRemote) Bootstrap(ctx context.Context, n model.Node, keyPath, pubkey, password string) error {
	return node.BootstrapNode(ctx, n, keyPath, pubkey, password)
}

func (nodeRemote) Run(ctx context.Context, n model.Node, a node.Auth, script string) (string, error) {
	return node.RunAs(ctx, n, a, script)
}

func (nodeRemote) RunIO(ctx context.Context, n model.Node, a node.Auth, script string, stdout, stderr io.Writer) error {
	return node.RunAsIO(ctx, n, a, script, stdout, stderr)
}

func (nodeRemote) RunWithInput(ctx context.Context, n model.Node, a node.Auth, script string, stdin []byte) (string, error) {
	return node.RunWithInputAs(ctx, n, a, script, stdin)
}

func (nodeRemote) PutFile(ctx context.Context, n model.Node, a node.Auth, p string, content []byte) error {
	return node.PutFileAs(ctx, n, a, p, content)
}

var initSSH initRemote = nodeRemote{}

var initConnect = connectAndSave
