package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

const stateDir = ".seedfall"

type namedInt struct {
	name string
	n    int
}

func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func checkJobs(n int) error {
	if n < 1 {
		return fmt.Errorf("-j must be at least 1, got %d", n)
	}
	return nil
}

func nonNegative(values ...namedInt) error {
	for _, v := range values {
		if v.n < 0 {
			return fmt.Errorf("%s cannot be negative, got %d", v.name, v.n)
		}
	}
	return nil
}

func checkBatch(batch int, given bool, untilNovelty int) error {
	if err := nonNegative(namedInt{"-batch", batch}); err != nil {
		return err
	}
	if given && untilNovelty <= 0 {
		return errors.New("-batch only applies with -until-novelty")
	}
	return nil
}

func checkMutants(mutants int, given, app bool) error {
	if err := nonNegative(namedInt{"-mutants", mutants}); err != nil {
		return err
	}
	if given && !app {
		return errors.New("-mutants only applies with -app")
	}
	return nil
}

func checkRepo(repo string) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !repoPart(owner) || !repoPart(name) {
		return fmt.Errorf("-repo %q must be owner/name, using letters, digits, '.', '_' and '-'", repo)
	}
	return nil
}

func repoPart(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	})
}
