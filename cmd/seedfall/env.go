package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const envHelp = `one JSON object per line on stdin, one JSON object per line on stdout:
  {"cmd":"reset","config":"seedfall.json","seed":1}      start an episode, paused at virtual time 0
  {"cmd":"step","op":{"kind":"crash","node":"n1"}}       apply an action now
  {"cmd":"step","advance":"2s"}                          let virtual time pass
  {"cmd":"observe"}                                      nodes, new log lines, messages in flight, client stats
  {"cmd":"state"}                                        the same without the log
  {"cmd":"finish"}                                       run to the end and return the verdict
  {"cmd":"verify","ops":[...],"seeds":[2,3]}             replay the actions on the episode's seed and these seeds`

type envRequest struct {
	Cmd     string      `json:"cmd"`
	Config  string      `json:"config,omitempty"`
	Profile string      `json:"profile,omitempty"`
	Set     []string    `json:"set,omitempty"`
	Seed    uint64      `json:"seed,omitempty"`
	Op      *engine.Op  `json:"op,omitempty"`
	Advance string      `json:"advance,omitempty"`
	Ops     []engine.Op `json:"ops,omitempty"`
	Seeds   []uint64    `json:"seeds,omitempty"`
}

type envResponse struct {
	OK          bool                `json:"ok"`
	Error       string              `json:"error,omitempty"`
	Observation *engine.Observation `json:"observation,omitempty"`
	Verdict     *envVerdict         `json:"verdict,omitempty"`
	Verify      *envVerify          `json:"verify,omitempty"`
}

type envProperty struct {
	Kind string `json:"kind"`
	Hits int    `json:"hits"`
	True int    `json:"true"`
}

type envLinearizability struct {
	Checked bool   `json:"checked"`
	Ok      bool   `json:"ok"`
	Ops     int    `json:"ops"`
	Summary string `json:"summary,omitempty"`
}

type envVerdict struct {
	Seed            uint64                 `json:"seed"`
	Passed          bool                   `json:"passed"`
	Finished        bool                   `json:"finished"`
	Failure         *engine.Failure        `json:"failure,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	At              engine.Duration        `json:"at"`
	Hash            string                 `json:"determinism_hash"`
	Properties      map[string]envProperty `json:"properties"`
	Linearizability envLinearizability     `json:"linearizability"`
	Ops             []engine.Op            `json:"ops"`
}

type envSeed struct {
	Seed    uint64 `json:"seed"`
	Passed  bool   `json:"passed"`
	Failure string `json:"failure,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Hash    string `json:"determinism_hash"`
}

type envVerify struct {
	Reward float64   `json:"reward"`
	Passed int       `json:"passed"`
	Seeds  []envSeed `json:"seeds"`
}

type envTarget struct {
	run *engine.Runner
	cfg *engine.Config
}

type envHost struct {
	jobs    int
	targets map[string]envTarget
	target  envTarget
	seed    uint64
	session *engine.Session
	logged  []engine.Op
}

func newEnvHost(jobs int) *envHost {
	return &envHost{jobs: jobs, targets: map[string]envTarget{}}
}

func cmdEnv(args []string) error {
	fs := newFlags("env")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel replays for verify")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: seedfall env [-j N]\n\n%s\n\nflags:\n", envHelp)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkJobs(*jobs); err != nil {
		return err
	}
	h := newEnvHost(*jobs)
	defer h.close()
	return h.serve(os.Stdin, os.Stdout)
}

func (h *envHost) serve(in io.Reader, out io.Writer) error {
	dec, enc := json.NewDecoder(bufio.NewReader(in)), json.NewEncoder(out)
	for {
		var req envRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := enc.Encode(h.handle(req)); err != nil {
			return err
		}
	}
}

func (h *envHost) handle(req envRequest) envResponse {
	res, err := h.dispatch(req)
	if err != nil {
		return envResponse{Error: err.Error()}
	}
	res.OK = true
	return res
}

func (h *envHost) dispatch(req envRequest) (envResponse, error) {
	switch req.Cmd {
	case "reset":
		return h.reset(req)
	case "verify":
		return h.verify(req)
	case "step", "observe", "state", "finish":
		if h.session == nil {
			return envResponse{}, errors.New("no episode: send reset first")
		}
	default:
		return envResponse{}, fmt.Errorf("unknown cmd %q", req.Cmd)
	}
	switch req.Cmd {
	case "step":
		return h.step(req)
	case "observe":
		return h.observed(h.session.Observe()), nil
	case "state":
		return h.observed(h.session.Peek()), nil
	}
	return h.finish()
}

func (h *envHost) observed(o engine.Observation) envResponse {
	return envResponse{Observation: &o}
}

func (h *envHost) close() {
	if h.session != nil {
		h.session.Close()
		h.session = nil
	}
}

func (h *envHost) load(ref engine.ConfigRef) (envTarget, error) {
	key := ref.String()
	if t, ok := h.targets[key]; ok {
		return t, nil
	}
	r, cfg, err := load(ref)
	if err != nil {
		return envTarget{}, err
	}
	h.targets[key] = envTarget{run: r, cfg: cfg}
	return h.targets[key], nil
}

func (h *envHost) reset(req envRequest) (envResponse, error) {
	ref := configArgs{Config: req.Config, Profile: req.Profile, Set: req.Set}.ref()
	t, err := h.load(ref)
	if err != nil {
		return envResponse{}, err
	}
	h.close()
	s, err := t.run.Operate(req.Seed, engine.Schedule{})
	if err != nil {
		return envResponse{}, err
	}
	h.target, h.seed, h.session, h.logged = t, req.Seed, s, nil
	return h.observed(s.Observe()), nil
}

func (h *envHost) step(req envRequest) (envResponse, error) {
	var err error
	switch {
	case (req.Op == nil) == (req.Advance == ""):
		err = errors.New("step takes either op or advance")
	case req.Op != nil:
		err = h.session.Do(*req.Op)
	default:
		var d time.Duration
		if d, err = time.ParseDuration(req.Advance); err == nil {
			err = h.session.Advance(d)
		}
	}
	if err != nil {
		return envResponse{}, err
	}
	return h.observed(h.session.Observe()), nil
}

func (h *envHost) finish() (envResponse, error) {
	h.logged = h.session.Ops()
	res := h.session.Finish()
	h.session = nil
	return envResponse{Verdict: verdictOf(res, h.logged)}, nil
}

func verdictOf(res engine.Result, ops []engine.Op) *envVerdict {
	v := &envVerdict{
		Seed: res.Seed, Passed: passed(res), Finished: res.Finished, Reason: res.Reason, At: engine.Duration(res.At), Hash: res.Hash(),
		Properties: map[string]envProperty{}, Ops: ops,
		Linearizability: envLinearizability{Checked: res.History.Checked, Ok: res.History.Ok, Ops: res.History.Count},
	}
	if res.History.Checked {
		v.Linearizability.Summary = res.History.Summary()
	}
	if res.Failed {
		v.Failure = &res.Failure
	}
	for name, p := range res.Properties {
		v.Properties[name] = envProperty{Kind: p.Kind.String(), Hits: p.Hits, True: p.True}
	}
	return v
}

func passed(res engine.Result) bool { return !res.Failed && res.Finished }

func (h *envHost) verify(req envRequest) (envResponse, error) {
	if h.target.run == nil {
		return envResponse{}, errors.New("no episode: send reset first")
	}
	ops := req.Ops
	switch {
	case ops != nil:
	case h.session != nil:
		ops = h.session.Ops()
	default:
		ops = h.logged
	}
	if err := h.target.cfg.CheckSchedule(engine.Schedule{Operator: ops}); err != nil {
		return envResponse{}, err
	}
	seeds := []uint64{h.seed}
	for _, s := range req.Seeds {
		if !slices.Contains(seeds, s) {
			seeds = append(seeds, s)
		}
	}
	return envResponse{Verify: h.replayAll(seeds, ops)}, nil
}

func (h *envHost) replayAll(seeds []uint64, ops []engine.Op) *envVerify {
	results := make([]engine.Result, len(seeds))
	slots := make(chan struct{}, h.jobs)
	var wg sync.WaitGroup
	for i, seed := range seeds {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			results[i] = h.target.run.Replay(seed, engine.Schedule{Operator: ops})
		})
	}
	wg.Wait()
	out := &envVerify{}
	for i, res := range results {
		ok := passed(res)
		if ok {
			out.Passed++
		}
		out.Seeds = append(out.Seeds, envSeed{Seed: seeds[i], Passed: ok, Failure: res.Failure.Key(), Reason: res.Reason, Hash: res.Hash()})
	}
	out.Reward = float64(out.Passed) / float64(len(seeds))
	return out
}

func readOps(path string, planned []engine.Op) ([]engine.Op, error) {
	if path == "" {
		return planned, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ops []engine.Op
	if err := json.Unmarshal(b, &ops); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ops, nil
}
