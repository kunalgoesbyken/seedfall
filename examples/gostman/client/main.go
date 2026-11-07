package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gostmankv"

	"github.com/kunalgoesbyken/gostman/engine"
	"github.com/kunalgoesbyken/gostman/runner"
	"github.com/kunalgoesbyken/seedfall/sim"
)

const workspace = "collection"

type contract struct {
	session    *runner.Session
	collection runner.Collection
	timeout    time.Duration
	retry      time.Duration
}

func main() {
	env := flag.String("env", "Simulation", "Gostman environment to run")
	writeFolder := flag.String("write", "Write", "Gostman folder sent while faults run; retried until it passes")
	verifyFolder := flag.String("verify", "Verify", "Gostman folder whose checks must pass once the faults stop")
	rounds := flag.Int("rounds", 10, "rounds of write, faults, verify")
	settle := flag.Duration("settle", time.Second, "time the faults get between a write and its verification")
	quiet := flag.Duration("quiet", time.Second, "fault-free window for each verification")
	timeout := flag.Duration("timeout", time.Second, "request timeout")
	retry := flag.Duration("retry", 50*time.Millisecond, "pause before resending a request that did not settle")
	flag.Parse()

	c, err := open(*env, *timeout, *retry)
	if err != nil {
		sim.Fail("cannot load the Gostman collection: " + err.Error())
	}
	write, verify := c.folder(*writeFolder), c.folder(*verifyFolder)
	for round := range *rounds {
		vars := map[string]string{"key": key(round), "value": fmt.Sprintf("v%d", round), "deleted_key": key(round - 1)}
		c.write(write, vars)
		time.Sleep(*settle)
		calm := time.Now().Add(*quiet)
		sim.QuietFor(*quiet)
		c.verify(verify, vars)
		time.Sleep(time.Until(calm))
	}
	fmt.Printf("%d rounds: every acknowledged write and delete survived the faults\n", *rounds)
}

func key(round int) string {
	return fmt.Sprintf("k%d", round)
}

func open(env string, timeout, retry time.Duration) (*contract, error) {
	if err := os.CopyFS(".", gostmankv.Collection); err != nil {
		return nil, err
	}
	collection, err := runner.Load(workspace)
	if err != nil {
		return nil, err
	}
	session, err := runner.NewSession(engine.Policy{})
	if err != nil {
		return nil, err
	}
	return &contract{session, collection, timeout, retry}, session.Enter(collection, "", env, nil)
}

func (c *contract) folder(name string) []runner.Listed {
	listed, err := c.session.Catalog(c.collection, name)
	if err != nil {
		sim.Fail("cannot list the Gostman folder " + name + ": " + err.Error())
	}
	for i := range listed {
		listed[i].Request.Options.TimeoutMs = int(c.timeout.Milliseconds())
	}
	return listed
}

func (c *contract) write(requests []runner.Listed, vars map[string]string) {
	for _, request := range requests {
		_, attempts := c.until(request, vars, passed)
		sim.Sometimes(attempts > 1, "a fault interrupted a Gostman write")
	}
}

func (c *contract) verify(requests []runner.Listed, vars map[string]string) {
	for _, request := range requests {
		result, _ := c.until(request, vars, answered)
		sim.Always(!result.Failed, "Gostman checks pass once the faults stop", broken(result, vars))
	}
}

func (c *contract) until(request runner.Listed, vars map[string]string, settled func(runner.Result) bool) (runner.Result, int) {
	for attempts := 1; ; attempts++ {
		result := c.session.Send(context.Background(), request.Request, request.Folder, vars).Result
		show(result)
		if settled(result) {
			return result, attempts
		}
		time.Sleep(c.retry)
	}
}

func show(result runner.Result) {
	fmt.Println(verdict(result), result.Method, result.QualifiedName(), result.Status)
}

func verdict(result runner.Result) string {
	if result.Failed {
		return "FAIL"
	}
	return "PASS"
}

func passed(result runner.Result) bool {
	return !result.Failed
}

func answered(result runner.Result) bool {
	return result.StatusCode != 0
}

func broken(result runner.Result, vars map[string]string) string {
	fill := strings.NewReplacer(placeholders(vars)...)
	lines := []string{fmt.Sprintf("%s %s (%s)", result.Method, result.ResolvedURL, result.QualifiedName())}
	for _, check := range result.Tests {
		if !check.Passed {
			lines = append(lines, fill.Replace(check.Name)+": "+check.Message)
		}
	}
	if result.Error != "" {
		lines = append(lines, result.Error)
	}
	return strings.Join(lines, "\n  ")
}

func placeholders(vars map[string]string) []string {
	pairs := make([]string, 0, 2*len(vars))
	for name, value := range vars {
		pairs = append(pairs, "{{"+name+"}}", value)
	}
	return pairs
}
