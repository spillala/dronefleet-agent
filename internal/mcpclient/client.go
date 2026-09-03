// Package mcpclient spawns dronefleet-mcp as a subprocess and speaks MCP to
// it over stdio, using the official Go SDK.
package mcpclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client wraps a live MCP session against a dronefleet-mcp subprocess.
type Client struct {
	session *mcp.ClientSession
}

// Connect starts serverPath as a child process (with env appended to the
// current environment) and completes the MCP handshake over its stdio.
func Connect(ctx context.Context, serverPath string, env []string) (*Client, error) {
	cmd := exec.Command(serverPath)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "dronefleet-agent", Version: "0.1.0"}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to dronefleet-mcp (%s): %w", serverPath, err)
	}
	return &Client{session: session}, nil
}

// Close ends the MCP session and terminates the subprocess.
func (c *Client) Close() error {
	return c.session.Close()
}

// ListTools returns every tool the server advertises.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	res, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return res.Tools, nil
}

// CallTool invokes a tool by name and returns its text content joined into
// a single string. If the tool reported an error, CallTool still returns
// whatever text it produced, alongside a non-nil error.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}

	var sb strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}

	if res.IsError {
		return sb.String(), fmt.Errorf("tool %s reported an error: %s", name, sb.String())
	}
	return sb.String(), nil
}
