package main

import (
	"flag"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
)

type configArgs struct {
	Config  string   `json:"config" jsonschema:"path to seedfall.json"`
	Profile string   `json:"profile,omitempty" jsonschema:"profile from the config's profiles map to merge over it"`
	Set     []string `json:"set,omitempty" jsonschema:"overrides applied last, each path=value, for example faults.crash_per_sec=0.5, faults=resilient or nodes.server.args=[\"-sync\",\"always\"]"`
}

func (a configArgs) ref() engine.ConfigRef {
	return engine.ConfigRef{Path: a.Config, Profile: a.Profile, Sets: a.Set}
}

type setFlag []string

func (s *setFlag) String() string { return strings.Join(*s, " ") }

func (s *setFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func configFlags(fs *flag.FlagSet) *configArgs {
	a := &configArgs{}
	a.register(fs)
	return a
}

func (a *configArgs) register(fs *flag.FlagSet) {
	fs.StringVar(&a.Config, "c", "seedfall.json", "config file")
	fs.StringVar(&a.Profile, "profile", "", "merge this profile from the config's profiles over it")
	fs.Var((*setFlag)(&a.Set), "set", "override one config value as path=value, applied last; repeatable, for example -set faults.crash_per_sec=0.5")
}
