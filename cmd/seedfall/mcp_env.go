package main

import (
	"context"
	"runtime"
	"sync"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type envResetArgs struct {
	configArgs
	Seed uint64 `json:"seed" jsonschema:"seed of the episode"`
}

type envStepArgs struct {
	Kind    string   `json:"kind,omitempty" jsonschema:"action to apply now: crash, restart, pause, resume, partition, heal, slow, clock_jump, set_args, upgrade or wait; leave empty to only advance"`
	Node    string   `json:"node,omitempty" jsonschema:"node the action applies to; partition, heal and wait take none"`
	Args    []string `json:"args,omitempty" jsonschema:"arguments of the action: restart takes an optional delay; partition a duration, then the nodes of one side; slow a factor, then a duration; clock_jump a shift; set_args the node's whole new argument list; wait a duration"`
	Advance string   `json:"advance,omitempty" jsonschema:"virtual time to let pass, for example 2s; give this or kind, not both"`
}

type envVerifyArgs struct {
	Seeds []uint64 `json:"seeds,omitempty" jsonschema:"extra seeds to replay the episode's logged actions against, besides the episode's own seed"`
}

type mcpEnv struct {
	mu   sync.Mutex
	host *envHost
}

func newMCPEnv() *mcpEnv { return &mcpEnv{host: newEnvHost(runtime.NumCPU())} }

func (e *mcpEnv) call(req envRequest) (*mcp.CallToolResult, any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res, err := e.host.dispatch(req)
	if err != nil {
		return nil, nil, err
	}
	res.OK = true
	return nil, res, nil
}

func (e *mcpEnv) reset(_ context.Context, _ *mcp.CallToolRequest, a envResetArgs) (*mcp.CallToolResult, any, error) {
	return e.call(envRequest{Cmd: "reset", Config: a.Config, Profile: a.Profile, Set: a.Set, Seed: a.Seed})
}

func (e *mcpEnv) step(_ context.Context, _ *mcp.CallToolRequest, a envStepArgs) (*mcp.CallToolResult, any, error) {
	req := envRequest{Cmd: "step", Advance: a.Advance}
	if a.Kind != "" {
		req.Op = &engine.Op{Kind: a.Kind, Node: a.Node, Args: a.Args}
	}
	return e.call(req)
}

func (e *mcpEnv) observe(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return e.call(envRequest{Cmd: "observe"})
}

func (e *mcpEnv) finish(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return e.call(envRequest{Cmd: "finish"})
}

func (e *mcpEnv) verify(_ context.Context, _ *mcp.CallToolRequest, a envVerifyArgs) (*mcp.CallToolResult, any, error) {
	return e.call(envRequest{Cmd: "verify", Seeds: a.Seeds})
}

func addEnvTools(server *mcp.Server) {
	env := newMCPEnv()
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_env_reset",
		Description: "Operator mode: start an episode, a live simulated cluster for one seed paused at virtual time 0, replacing any episode in progress. Returns the first observation. Act on it with seedfall_env_step, then call seedfall_env_finish.",
	}, env.reset)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_env_step",
		Description: "Operator mode: apply one action to the live cluster now (crash, restart, pause, resume, partition, heal, slow, clock_jump, set_args, upgrade, wait) or let virtual time pass with advance. Returns the observation: each node's state and arguments, the log lines since the last observation, messages in flight and the client's operation counts.",
	}, env.step)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_env_observe",
		Description: "Operator mode: read the cluster without changing it: node states, log lines since the last observation, messages in flight and the client's operation counts.",
	}, env.observe)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_env_finish",
		Description: "Operator mode: run the episode to its end and return the verdict: pass or fail, the failure and its reason, every property's hits, the linearizability check, the determinism hash and the logged actions.",
	}, env.finish)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_env_verify",
		Description: "Operator mode: replay the episode's logged actions against its seed and the given extra seeds, which can differ from the ones the operator saw. Returns each seed's pass or fail and the reward, the fraction that passed.",
	}, env.verify)
}
