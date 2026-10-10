package node

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/RovenEverward/my-proxy/internal/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
)

// StatsDialer forwards a loopback API through the existing SSH identity/config.
// No control binary or credentials are installed on the node.
type StatsDialer interface {
	DialStats(context.Context, model.Node, int) (net.Conn, error)
}

func (s SSH) DialStats(ctx context.Context, n model.Node, port int) (net.Conn, error) {
	args := sshArgsWithIdentity(n, "")
	target := args[len(args)-1]
	args = append(args[:len(args)-1], "-W", "127.0.0.1:"+strconv.Itoa(port), target)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	client, bridge := net.Pipe()
	conn := &sshStatsConn{Conn: client, cmd: cmd, bridge: bridge}
	go func() { _, _ = io.Copy(input, bridge); _ = input.Close() }()
	go func() { _, _ = io.Copy(bridge, output); _ = bridge.Close() }()
	go func() { _ = cmd.Wait(); _ = bridge.Close() }()
	return conn, nil
}

type sshStatsConn struct {
	net.Conn
	cmd    *exec.Cmd
	bridge net.Conn
	once   sync.Once
}

func (c *sshStatsConn) Close() error {
	c.once.Do(func() {
		_ = c.Conn.Close()
		_ = c.bridge.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
	return nil
}

type statsQuery struct{}
type statsReply struct{ Traffic map[string]UserTraffic }

// statsCodec implements the public V2Ray QueryStats protobuf wire format.
// sing-box v1.14.3 serves the v2ray.core namespace despite its generated client's
// experimental.v2rayapi namespace; the official server descriptor is authoritative.
type statsCodec struct{}

func (statsCodec) Name() string { return "proto" }
func (statsCodec) Marshal(v any) ([]byte, error) {
	if _, ok := v.(*statsQuery); !ok {
		return nil, fmt.Errorf("unexpected stats request %T", v)
	}
	return nil, nil
}
func (statsCodec) Unmarshal(data []byte, v any) error {
	out, ok := v.(*statsReply)
	if !ok {
		return fmt.Errorf("unexpected stats reply %T", v)
	}
	out.Traffic = make(map[string]UserTraffic)
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		if number != 1 || kind != protowire.BytesType {
			n = protowire.ConsumeFieldValue(number, kind, data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			continue
		}
		record, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		var name string
		var value int64
		for len(record) > 0 {
			f, k, m := protowire.ConsumeTag(record)
			if m < 0 {
				return protowire.ParseError(m)
			}
			record = record[m:]
			if f == 1 && k == protowire.BytesType {
				val, m := protowire.ConsumeString(record)
				if m < 0 {
					return protowire.ParseError(m)
				}
				name = val
				record = record[m:]
			} else if f == 2 && k == protowire.VarintType {
				val, m := protowire.ConsumeVarint(record)
				if m < 0 {
					return protowire.ParseError(m)
				}
				value = int64(val)
				record = record[m:]
			} else {
				m = protowire.ConsumeFieldValue(f, k, record)
				if m < 0 {
					return protowire.ParseError(m)
				}
				record = record[m:]
			}
		}
		if value < 0 {
			return fmt.Errorf("negative traffic counter %q", name)
		}
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" || (parts[3] != "uplink" && parts[3] != "downlink") {
			continue
		}
		user, direction := parts[1], parts[3]
		traffic := out.Traffic[user]
		traffic.Username = user
		if direction == "uplink" {
			traffic.Uplink = value
		} else {
			traffic.Downlink = value
		}
		out.Traffic[user] = traffic
	}
	return nil
}

func QuerySingBoxTraffic(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment) (map[string]UserTraffic, error) {
	dialer, ok := r.(StatsDialer)
	if !ok {
		return nil, fmt.Errorf("runner does not support SSH stats forwarding")
	}
	conn, err := grpc.NewClient("passthrough:///sing-box-stats", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
		return dialer.DialStats(ctx, n, d.APIPortOrDefault())
	}), grpc.WithDefaultCallOptions(grpc.ForceCodec(statsCodec{})))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	reply := new(statsReply)
	if err := conn.Invoke(ctx, "/v2ray.core.app.stats.command.StatsService/QueryStats", &statsQuery{}, reply); err != nil {
		return nil, fmt.Errorf("sing-box individual statistics: %w", err)
	}
	return reply.Traffic, nil
}
