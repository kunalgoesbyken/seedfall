package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var collectFlags func(*flag.FlagSet)

type flagView struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	Usage   string `json:"usage"`
}

type commandView struct {
	Command  string     `json:"command"`
	Synopsis string     `json:"synopsis"`
	Flags    []flagView `json:"flags"`
}

type surfaceView struct {
	Version   string          `json:"version"`
	Commands  []commandView   `json:"commands"`
	Tools     []*mcp.Tool     `json:"mcp_tools"`
	Prompts   []*mcp.Prompt   `json:"mcp_prompts"`
	Resources []*mcp.Resource `json:"mcp_resources"`
}

func cmdSchema(args []string) error {
	fs := newFlags("schema")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := surface()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func surface() (surfaceView, error) {
	out := surfaceView{Version: engine.Version()}
	for _, c := range commands {
		flags, err := commandFlags(c)
		if err != nil {
			return out, err
		}
		out.Commands = append(out.Commands, commandView{Command: "seedfall " + c.name, Synopsis: c.synopsis, Flags: flags})
	}
	server, err := newMCPServer()
	if err != nil {
		return out, err
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		return out, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "seedfall schema"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return out, err
	}
	defer cs.Close()
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return out, err
		}
		out.Tools = append(out.Tools, t)
	}
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			return out, err
		}
		out.Prompts = append(out.Prompts, p)
	}
	for r, err := range cs.Resources(ctx, nil) {
		if err != nil {
			return out, err
		}
		out.Resources = append(out.Resources, r)
	}
	return out, nil
}

func commandFlags(c command) ([]flagView, error) {
	var fs *flag.FlagSet
	collectFlags = func(f *flag.FlagSet) { fs = f }
	defer func() { collectFlags = nil }()
	if err := c.run([]string{"-h"}); !errors.Is(err, flag.ErrHelp) || fs == nil {
		return nil, fmt.Errorf("seedfall %s does not describe its flags: %v", c.name, err)
	}
	flags := []flagView{}
	fs.VisitAll(func(f *flag.Flag) {
		kind, usage := flag.UnquoteUsage(f)
		if kind == "" {
			kind = "bool"
		}
		flags = append(flags, flagView{Name: f.Name, Type: kind, Default: f.DefValue, Usage: strings.TrimSpace(usage)})
	})
	return flags, nil
}
